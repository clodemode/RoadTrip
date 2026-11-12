package main

// What was played. One row per track that started sounding on the signed-in
// driver's player, kept so anyone can see what is playing now and what played
// on the trip (Kris, 2026-11-12). Everything here is text and numbers about the
// music: no audio, no library address, no key, no album number (that number is
// only useful with the library's key), and no user name leaves this file.
//
// "Sound started" is the player's own `playing` event for a track it has just
// loaded, reported once with the resume post (POST /driver/roadtracks/state with
// `started`, `title` and `year`). A resume, a pause and a retry never start a
// track, and a retry carries the same `started` number, so the unique key
// (user, album, track, started) makes it one row. A client that sends no
// `started` (an older page) records nothing.

import (
	"net/http"
	"strconv"
	"time"
)

const (
	// nowPlayingFresh is how long after a track started it is still "now". Most
	// tracks are shorter, and a newer row replaces it the moment the next track
	// starts; ten minutes keeps a long track current without leaving a stopped
	// player on "now" for the rest of the day.
	nowPlayingFresh = 10 * time.Minute
	tracksListCap   = 200
	// playPointFresh is how old a stored point may be and still stand for where
	// the car was when a track started.
	playPointFresh = 3 * time.Hour
	playNoteMax    = 300
)

// Play is one track that started sounding.
type Play struct {
	ID        int64
	PubID     string // opaque public name; see listen.go
	Ride      string
	User      string
	AlbumID   int64
	Album     string
	Artist    string
	Year      int
	TrackIdx  int
	Title     string
	StartedAt string // RFC3339 UTC
	TZ        string
	Note      string
	Lat, Lng  *float64
	StartKey  int64
}

// playView is a play as the public page and /tracks.json show it.
type playView struct {
	ID        string   `json:"id,omitempty"` // opaque: names the play to /tracks/{id}/audio, never the album
	Track     string   `json:"track"`
	Album     string   `json:"album"`
	Artist    string   `json:"artist"`
	Year      int      `json:"year,omitempty"`
	StartedAt string   `json:"started_at"`
	TZ        string   `json:"tz"`
	Note      string   `json:"note,omitempty"`
	Lat       *float64 `json:"lat,omitempty"`
	Lng       *float64 `json:"lng,omitempty"`
}

// tracksDoc is the whole public answer: now, or last when nothing is current.
type tracksDoc struct {
	Listen bool       `json:"listen"` // whether a visitor may hear the tracks (PUBLIC_LISTEN)
	Now    *playView  `json:"now"`
	Last   *playView  `json:"last"`
	Tracks []playView `json:"tracks"`
}

func (p Play) view() playView {
	return playView{ID: p.PubID, Track: p.Title, Album: p.Album, Artist: p.Artist, Year: p.Year, StartedAt: p.StartedAt,
		TZ: p.TZ, Note: p.Note, Lat: p.Lat, Lng: p.Lng}
}

// AddPlay records one start; the same start reported again is ignored.
func (s *Store) AddPlay(p Play) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO plays(ride_slug, user, album_id, album, artist, year, track_idx, title, started_at, tz, note, lat, lng, start_key, pub_id)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?, lower(hex(randomblob(8))))`, p.Ride, p.User, p.AlbumID, p.Album, p.Artist, p.Year, p.TrackIdx, p.Title, p.StartedAt, p.TZ, p.Note, p.Lat, p.Lng, p.StartKey)
	return err
}

// Plays is a ride's plays, newest first, at most limit. User and the album
// number are read only to be dropped by view(); nothing selects them for a page.
func (s *Store) Plays(ride string, limit int) ([]Play, error) {
	rows, err := s.db.Query(`SELECT id, pub_id, album, artist, year, title, started_at, tz, note, lat, lng FROM plays
WHERE ride_slug = ? ORDER BY started_at DESC, id DESC LIMIT ?`, ride, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Play
	for rows.Next() {
		var p Play
		if err := rows.Scan(&p.ID, &p.PubID, &p.Album, &p.Artist, &p.Year, &p.Title, &p.StartedAt, &p.TZ, &p.Note, &p.Lat, &p.Lng); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// tracksDocOf is the pure shaping: the newest play is `now` while it is fresh,
// else it is `last`.
func tracksDocOf(plays []Play, now time.Time) *tracksDoc {
	doc := &tracksDoc{Tracks: make([]playView, 0, len(plays))}
	for _, p := range plays {
		doc.Tracks = append(doc.Tracks, p.view())
	}
	if len(plays) == 0 {
		return doc
	}
	v := doc.Tracks[0]
	if at, ok := parseUTC(v.StartedAt); ok && now.Sub(at) < nowPlayingFresh && now.Sub(at) > -time.Minute {
		doc.Now = &v
	} else {
		doc.Last = &v
	}
	return doc
}

// nowPlayingFor is the document for a ride, or nil when it has no plays (the
// page then shows no chip and no Listen Inn).
func (a *App) nowPlayingFor(ride string, now time.Time) *tracksDoc {
	plays, err := a.store.Plays(ride, tracksListCap)
	if err != nil || len(plays) == 0 {
		return nil
	}
	doc := tracksDocOf(plays, now)
	doc.Listen = a.listenOn()
	return doc
}

// tracksJSON: GET /tracks.json, open to a visitor like the home page.
func (a *App) tracksJSON(w http.ResponseWriter, r *http.Request) {
	doc := a.nowPlayingFor(a.activeSlug(), time.Now())
	if doc == nil {
		doc = &tracksDoc{Tracks: []playView{}, Listen: a.listenOn()}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, doc)
}

// startedPlay is what the resume post says about a track that has just started.
type startedPlay struct {
	albumID  int64
	album    string
	artist   string
	year     int
	idx      int
	title    string
	startKey int64
}

// startedOf reads the optional start fields. ok is false for an older client
// (no `started`) or for a start with no title, which records nothing.
func startedOf(r *http.Request, albumID int64, idx int) (startedPlay, bool) {
	k, err := strconv.ParseInt(r.PostFormValue("started"), 10, 64)
	title := clip(r.PostFormValue("title"), 200)
	if err != nil || k <= 0 || k > 1<<53 || title == "" {
		return startedPlay{}, false
	}
	year, _ := strconv.Atoi(r.PostFormValue("year"))
	if year < 0 || year > 3000 {
		year = 0
	}
	return startedPlay{albumID: albumID, album: clip(r.PostFormValue("album"), 200), artist: clip(r.PostFormValue("artist"), 200),
		year: year, idx: idx, title: title, startKey: k}, true
}

// recordPlay keeps one start. The note is Chester's own reason from a Drive
// Tracks pick of this album on this ride, never made up. The point is the
// newer of that pick's stored point (already through publishPosition when it
// was logged) and the ride's newest pinned post, which goes through
// publishPosition here; a play with neither, or only an old one, has none.
// Nothing is logged: a place or a note must not reach a log line.
func (a *App) recordPlay(user string, sp startedPlay, now time.Time) error {
	rv, err := a.loadRide()
	if err != nil {
		return err
	}
	p := Play{Ride: rv.Ride.Slug, User: user, AlbumID: sp.albumID, Album: sp.album, Artist: sp.artist, Year: sp.year,
		TrackIdx: sp.idx, Title: sp.title, StartedAt: utcString(now),
		TZ: defaultTZ(rv.Legs, defaultLeg(rv.Legs, dayOf(rv.Ride, rv.Legs, now))), StartKey: sp.startKey}
	var pointAt time.Time
	if rows, err := a.store.ChesterQueriesOf(user); err == nil {
		for _, q := range rows { // newest first
			if q.Preset != chesterDriveTracksPreset || q.Ride != rv.Ride.Slug {
				continue
			}
			for _, c := range q.Music {
				if c.AlbumID != sp.albumID || sp.albumID <= 0 {
					continue
				}
				p.Note = cleanLine(c.Why, playNoteMax)
				if p.Year == 0 {
					p.Year = c.Year
				}
				if at, ok := parseUTC(q.AskedAt); ok && q.PosLat != nil && q.PosLng != nil {
					p.Lat, p.Lng, pointAt = q.PosLat, q.PosLng, at
				}
				break
			}
			if p.Note != "" {
				break
			}
		}
	}
	if posts, err := a.store.Posts(rv.Ride.Slug, 0); err == nil {
		for _, ps := range posts {
			if ps.Lat == nil || ps.Lng == nil || ps.HideLocation {
				continue
			}
			if at, ok := parseUTC(ps.OccurredAt); ok && at.After(pointAt) {
				pub, _ := publishPosition(*ps.Lat, *ps.Lng, rideCheckpoints(rv.Legs))
				p.Lat, p.Lng, pointAt = &pub.Lat, &pub.Lng, at
			}
			break
		}
	}
	if p.Lat != nil && now.Sub(pointAt) > playPointFresh {
		p.Lat, p.Lng = nil, nil
	}
	return a.store.AddPlay(p)
}
