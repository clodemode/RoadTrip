package main

import (
	"fmt"
	"strings"
	"time"
)

// An old photo with no place of its own. The phone that uploads a photo is not
// where the photo was taken: a photo whose own time is more than oldPhotoAfter
// before now and which carries no GPS says nothing about where the phone is,
// so the phone, and the simulated car that stands for it on a KATE, are never
// used for it. It is pinned from the car's own trail at the photo's time or
// not at all, keeps its own time, and never vouches a check-in.
//
// The trail is the published pins of earlier posts. Nothing else on the road
// holds a position over time: telemetry goes to the bus with no coordinate
// (telemetry.go), and the simulated car is a KATE-only clock with no history.

// pinTrail is a pin taken from the trail, not from a fix made at the photo.
const pinTrail = "trail"

const (
	// oldPhotoAfter: a photo older than this, with no GPS, is not "here".
	oldPhotoAfter = 20 * time.Minute
	// trailWindow is how near in time to the photo a trail point must be.
	trailWindow = 15 * time.Minute
)

// The Flag words an old photo's post carries: it was old when posted, and its
// pin (if any) came from the trail.
const (
	flagOldPhoto = "old_photo"
	flagTrailPin = "trail_pin"
)

// oldPhotoTime is the photo's own time when the photo is an old one with no
// GPS of its own; ok is false for every other post (no photo, no time, a
// time within oldPhotoAfter, a GPS of its own). A time in the future is not
// old.
func oldPhotoTime(photo *Photo, now time.Time) (taken time.Time, ok bool) {
	if photo == nil || photo.TakenAt == "" || (photo.Lat != nil && photo.Lng != nil) {
		return time.Time{}, false
	}
	t, ok := parseUTC(photo.TakenAt)
	if !ok || now.Sub(t) <= oldPhotoAfter {
		return time.Time{}, false
	}
	return t, true
}

// trailNear is the car's published position nearest in time to at, within
// trailWindow: the pin of an earlier post of the ride that is a real fix (the
// photo's GPS, the phone or the simulated car), not a hidden post, not a
// checkpoint's own point and not another trail pin. A pin the store already
// coarsened is as coarse here.
func (s *Store) trailNear(rideSlug string, at time.Time) (lat, lng float64, ok bool) {
	rows, err := s.db.Query(`SELECT p.lat, p.lng, p.occurred_at
	  FROM posts p JOIN legs l ON l.id=p.leg_id
	  WHERE l.ride_slug=? AND p.hide_location=0 AND p.lat IS NOT NULL AND p.lng IS NOT NULL
	    AND p.pin_source IN ('exif','browser','sim')`, rideSlug)
	if err != nil {
		return 0, 0, false
	}
	defer rows.Close()
	best := trailWindow + 1
	for rows.Next() {
		var la, ln float64
		var when string
		if rows.Scan(&la, &ln, &when) != nil {
			continue
		}
		t, tok := parseUTC(when)
		if !tok {
			continue
		}
		d := t.Sub(at)
		if d < 0 {
			d = -d
		}
		if d <= trailWindow && d < best {
			best, lat, lng, ok = d, la, ln, true
		}
	}
	return lat, lng, ok
}

// ageWords says how long ago, in the unit a driver would: minutes under an
// hour, hours under two days, days after.
func ageWords(d time.Duration) string {
	plural := func(n int, unit string) string {
		if n == 1 {
			return fmt.Sprintf("1 %s", unit)
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}
	switch {
	case d < time.Hour:
		return plural(int(d.Round(time.Minute)/time.Minute), "minute")
	case d < 48*time.Hour:
		return plural(int(d.Round(time.Hour)/time.Hour), "hour")
	}
	return plural(int(d.Round(24*time.Hour)/(24*time.Hour)), "day")
}

// oldPhotoNotice is what the post form says after an old photo is posted.
func oldPhotoNotice(age time.Duration, pinned, fromTrail bool) string {
	head := "Posted at its own time: the photo was taken " + ageWords(age) + " ago"
	switch {
	case pinned && fromTrail:
		return head + ", so it is pinned where the car was then, and nothing was checked in"
	case pinned:
		return head + ", so it is pinned where you chose, and nothing was checked in"
	}
	return head + " and the car's trail has nothing near then, so it is left off the map, and nothing was checked in"
}

func hasFlag(flag, word string) bool {
	for _, w := range strings.Fields(flag) {
		if w == word {
			return true
		}
	}
	return false
}

// oldPhotoAge is how long before it was posted an old photo was taken, as the
// signed-in pin line says it; empty when the post has no usable times.
func oldPhotoAge(p Post) string {
	at, ok1 := parseUTC(p.OccurredAt)
	made, ok2 := parseUTC(p.CreatedAt)
	if !ok1 || !ok2 || made.Before(at) {
		return ""
	}
	return ageWords(made.Sub(at))
}
