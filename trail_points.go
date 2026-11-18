package main

import (
	"time"
)

// Trail points. A Chester ask and a track play each say where the car is, and
// until 2026-11-14 neither left a mark: the been-here line read only posts.
// A trail point is one row (ride, leg, time, tz, point, source), written when
// the phone sent a position with an ask or a play start. It is not a post: it
// has no title, no photo, no pin on the map, and nothing lists it.
//
// The point stored is the PUBLISHED one. dropTrailPoint runs the phone's point
// through publishPosition (the store's fuzz seam) before the row is written, so
// a private place's point is its snapped cell and every other point is rounded
// to three decimals. No raw coordinate is stored, logged or rendered.

const (
	trailSourceChester = "chester"
	trailSourcePlay    = "play"
	// trailSourcePin is a Drop Pin tap (drop_pin.go): the one trail point that
	// is drawn on the map, as a dot.
	trailSourcePin = "pin"
)

// TrailPoint is one stored point of the car's trail.
type TrailPoint struct {
	ID         int64
	RideSlug   string
	LegID      int64
	OccurredAt string // RFC3339 UTC
	TZ         string
	Lat, Lng   float64
	Source     string
}

// AddTrailPoint stores a point that is ALREADY published. Callers go through
// dropTrailPoint, which is the only one that publishes it.
func (s *Store) AddTrailPoint(t TrailPoint) error {
	_, err := s.db.Exec(`INSERT INTO trail_points(ride_slug, leg_id, occurred_at, tz, lat, lng, source) VALUES(?,?,?,?,?,?,?)`,
		t.RideSlug, t.LegID, t.OccurredAt, t.TZ, t.Lat, t.Lng, t.Source)
	return err
}

// TrailPoints is a ride's trail points, oldest first.
func (s *Store) TrailPoints(rideSlug string) ([]TrailPoint, error) {
	rows, err := s.db.Query(`SELECT id, ride_slug, leg_id, occurred_at, tz, lat, lng, source FROM trail_points
	  WHERE ride_slug=? ORDER BY occurred_at, id`, rideSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrailPoint
	for rows.Next() {
		var t TrailPoint
		if err := rows.Scan(&t.ID, &t.RideSlug, &t.LegID, &t.OccurredAt, &t.TZ, &t.Lat, &t.Lng, &t.Source); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// dropTrailPoint records where the car is, from a phone position that came with
// an ask or a play start. The point goes through publishPosition first. A ride
// that cannot be read or a write that fails drops nothing and is no error to
// the caller: a trail point is a courtesy to the map, never a reason to fail an
// ask or a play. Nothing is logged: a point must not reach a log line.
// It answers whether a point was stored, for the one caller that tells the
// phone (Drop Pin).
func (a *App) dropTrailPoint(lat, lng float64, source string, now time.Time) bool {
	rv, err := a.loadRide()
	if err != nil || len(rv.Legs) == 0 || rideFinished(rv.Legs) {
		return false
	}
	leg := defaultLeg(rv.Legs, dayOf(rv.Ride, rv.Legs, now))
	if leg == nil {
		return false
	}
	pub, _ := publishPosition(lat, lng, rideCheckpoints(rv.Legs))
	return a.store.AddTrailPoint(TrailPoint{RideSlug: rv.Ride.Slug, LegID: leg.ID, OccurredAt: utcString(now),
		TZ: defaultTZ(rv.Legs, leg), Lat: pub.Lat, Lng: pub.Lng, Source: source}) == nil
}

// withTrail is the posts a track is drawn from, plus the trail points as bare
// pinned posts: a leg, a time and a published point, nothing else. They are for
// driven() alone; the map's pins and every list still read the real posts.
func (a *App) withTrail(rideSlug string, posts []Post) []Post {
	pts, err := a.store.TrailPoints(rideSlug)
	if err != nil || len(pts) == 0 {
		return posts
	}
	out := append([]Post(nil), posts...)
	for _, t := range pts {
		la, ln := t.Lat, t.Lng
		out = append(out, Post{LegID: t.LegID, OccurredAt: t.OccurredAt, TZ: t.TZ, PinLat: &la, PinLng: &ln, PinSource: "trail_" + t.Source})
	}
	return out
}
