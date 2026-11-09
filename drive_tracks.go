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
	driveEarlierMax          = 6             // earlier picks named, newest first
	driveTracksQuestionIntro = "Pick one album from the music library for this moment of the drive. Use where the car is, what the drive is doing now and what the traveller last asked you. In one turn call search_music several times, once for each of several artists that suit this moment, then call show_music with exactly one album, then say in one short sentence why it suits. Do not search the web. The album starts by itself on the driver screen, so do not tell the traveller to tap Play."
	driveTracksTypedPrefix   = "The traveller last typed: "
	driveTracksEarlierPrefix = "Albums already picked today, so pick a different one: "
)

// driveTracksQuestion is the one question a Drive Tracks tap sends. It adds
// the newest question this driver typed to Chester (a row with no preset)
// when it is under two hours old, and the albums already picked today.
func (a *App) driveTracksQuestion(user string, now time.Time) string {
	q := driveTracksQuestionIntro
	rows, err := a.store.ChesterQueriesOf(user)
	if err != nil {
		return q
	}
	typed, day := "", now.UTC().Format(dateLayout)
	var earlier []string
	for _, r := range rows { // newest first
		at, perr := time.Parse(time.RFC3339, r.AskedAt)
		if r.Preset == "" && typed == "" && perr == nil && now.Sub(at) < driveTypedFresh && now.Sub(at) >= 0 {
			typed = cleanLine(r.Question, driveTypedMax)
		}
		if r.Preset == chesterDriveTracksPreset && strings.HasPrefix(r.AskedAt, day) && len(earlier) < driveEarlierMax {
			for _, c := range r.Music {
				earlier = append(earlier, cleanLine(c.Album, 120)+" by "+cleanLine(c.Artist, 120))
			}
		}
	}
	if typed != "" {
		q += "\n" + driveTracksTypedPrefix + typed
	}
	if len(earlier) > 0 {
		q += "\n" + driveTracksEarlierPrefix + strings.Join(earlier, "; ")
	}
	return q
}
