package main

import (
	"strings"
	"time"
)

// Drive Tracks. One tap on the driver screen asks Chester for one album from
// the music library that fits the moment, and the card player on /driver plays
// it (Kris, 2026-11-08: it plays in RoadTrip, it does not open the library).
// The tap posts source=drivetracks to /chester/ask. The words Chester is sent
// are built here, never by the page, and the row is logged with the preset
// "drivetracks", which is not one of chesterPresets, so the question is never
// a button on the Chester page. Music starts only from the tap, never by itself.

const (
	chesterSourceDrive       = "drivetracks"
	chesterDriveTracksPreset = "drivetracks"
	driveTracksSaidDown      = "Music library unavailable"
	driveTypedFresh          = 2 * time.Hour // a typed question older than this is not mentioned
	driveTypedMax            = 300           // bytes of the typed question that are passed on
	driveEarlierMax          = 60            // earlier Drive Tracks picks on this trip that are avoided, newest first
	driveEarlierBytes        = 2400          // bytes of album names the question may carry
	driveTracksQuestionIntro = "Pick one album from the music library for this moment of the drive. Use where the car is, what the drive is doing now and what the traveller last asked you. In one turn call search_music several times, once for each of several artists that suit this moment, then call show_music with exactly one album, then say in one short sentence why it suits. Do not search the web. The album starts by itself on the driver screen, so do not tell the traveller to tap Play."
	driveTracksTypedPrefix   = "The traveller last typed: "
	driveTracksEarlierPrefix = "Albums already played on this trip, so pick a different one: "
	driveTracksAllPlayed     = "Every match has already been played on this trip. Search a different artist."
)

// driveTracksQuestion is the one question a Drive Tracks tap sends, and the
// albums the server will not let Chester pick. It adds the newest question
// this driver typed to Chester (a row with no preset) when it is under two
// hours old. The albums to avoid are every album Drive Tracks already picked
// on the active trip (any day, up to driveEarlierMax picks), the album in the
// resume point and the albums in the track marks. They are named in the
// question, newest first, and also returned as ids: the search answer drops
// them before Chester sees them (Kris, 2026-11-10: the same albums came back
// on one trip as soon as they fell out of the six newest of the day).
func (a *App) driveTracksQuestion(user string, now time.Time) (string, map[int64]bool) {
	q := driveTracksQuestionIntro
	avoid := map[int64]bool{}
	var earlier []string
	add := func(id int64, album, artist string) {
		if id <= 0 || avoid[id] {
			return
		}
		avoid[id] = true
		earlier = append(earlier, cleanLine(album, 120)+" by "+cleanLine(artist, 120))
	}
	typed, ride, picks := "", a.activeSlug(), 0
	if rows, err := a.store.ChesterQueriesOf(user); err == nil {
		for _, r := range rows { // newest first
			at, perr := time.Parse(time.RFC3339, r.AskedAt)
			if r.Preset == "" && typed == "" && perr == nil && now.Sub(at) < driveTypedFresh && now.Sub(at) >= 0 {
				typed = cleanLine(r.Question, driveTypedMax)
			}
			if r.Preset == chesterDriveTracksPreset && ride != "" && r.Ride == ride && picks < driveEarlierMax {
				picks++
				for _, c := range r.Music {
					add(c.AlbumID, c.Album, c.Artist)
				}
			}
		}
	}
	if rv, err := a.store.PlayerState(user, now); err == nil && rv != nil {
		add(rv.AlbumID, rv.Album, rv.Artist)
	}
	for _, m := range a.trackMarkAlbums(user) {
		add(m.AlbumID, m.Album, m.Artist)
	}
	if typed != "" {
		q += "\n" + driveTracksTypedPrefix + typed
	}
	if len(earlier) > 0 {
		named, size := earlier[:0:0], 0
		for _, e := range earlier {
			if size+len(e)+2 > driveEarlierBytes {
				break
			}
			named, size = append(named, e), size+len(e)+2
		}
		q += "\n" + driveTracksEarlierPrefix + strings.Join(named, "; ")
	}
	return q, avoid
}

// trackMarkAlbums is each album the user marked, once. A read failure is none.
func (a *App) trackMarkAlbums(user string) []resumeView {
	rows, err := a.store.db.Query(`SELECT album_id, album, artist FROM track_marks WHERE user = ? GROUP BY album_id ORDER BY MAX(id) DESC`, user)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []resumeView
	for rows.Next() {
		var v resumeView
		if rows.Scan(&v.AlbumID, &v.Album, &v.Artist) == nil {
			out = append(out, v)
		}
	}
	return out
}

// driveTracksMelt records what one Drive Tracks tap came to: the album picked,
// how many albums were avoided and whether the pick was one of them (which the
// search answer should make impossible), or why no album came out.
func (a *App) driveTracksMelt(avoid map[int64]bool, res chesterResult) {
	if len(res.Music) > 0 {
		id := res.Music[0].AlbumID
		a.melt.Event("drivetracks_pick", map[string]any{"album_id": id, "avoided": len(avoid), "repeat": avoid[id]})
		return
	}
	reason := "none"
	switch {
	case res.Err != "":
		reason = "error"
	case res.MusicDown:
		reason = "down"
	}
	a.melt.Error("drivetracks_no_pick", map[string]any{"reason": reason, "avoided": len(avoid)})
}
