package main

import (
	"log"
	"math"
	"net/http"
	"time"
)

// A stamp is caused by the car (Kris, 2026-11-18). Opening the trip is not the
// departure: "Start the drive" stores opened_at on the ride and stamps
// nothing. The start's departure stamp is written by the first position that
// shows the car has left the start, or by the Driving now button for a phone
// that sends no position. Drive time, ETAs and leg timing read the stamp, so
// they count from the departure alone.

// startLeaveKm is how far from the start a position must be to say the car has
// left it. Half a kilometre is past a driveway and a car park, and well inside
// any fix's honest error on the road.
const startLeaveKm = 0.5

// metaOpenedKey is where a ride's opened_at is kept, in meta, so the
// checkpoints table is untouched.
func metaOpenedKey(slug string) string { return "opened:" + slug }

// tripOpened says whether the ride was opened with a Start post.
func (a *App) tripOpened(slug string) bool {
	v, ok := a.store.Meta(metaOpenedKey(slug))
	return ok && v != ""
}

// openTrip records that the ride was opened, at the first time only.
func (a *App) openTrip(slug string, at time.Time) error {
	if a.tripOpened(slug) {
		return nil
	}
	return a.store.SetMeta(metaOpenedKey(slug), utcString(at))
}

// maybeDepart is the one departure check every stored position calls: a post's
// vouched position, a Drop Pin, a trail point from a play or a Chester ask. When
// the trip is open, the start has no stamp and the position lies more than
// startLeaveKm from the start, the start is stamped at the position's own time.
// It says whether it stamped. Names and kilometres go to the bus, never a point.
func (a *App) maybeDepart(lat, lng float64, at time.Time, src string) bool {
	rv, err := a.loadRide()
	if err != nil {
		return false
	}
	st := tripStart(rv.Legs)
	if st == nil || st.ArrivedAt != "" || !a.tripOpened(rv.Ride.Slug) {
		return false
	}
	km := haversineKm(lat, lng, st.Lat, st.Lng)
	if km <= startLeaveKm {
		return false
	}
	if err := a.arriveCheckpoint(st, at, nil); err != nil {
		log.Printf("departure: %v", err)
		return false
	}
	a.melt.Event("drive_started", map[string]any{"vouched_by": src, "km": math.Round(km*10) / 10})
	log.Printf("drive started from %s by %s", st.Name, src)
	return true
}

// rideDepart is Driving now: the departure stamped at the tap, for a phone that
// sends no position. A person's explicit tap is its own cause. It opens the
// trip too, so a trip never opened with a photo can still be set going.
func (a *App) rideDepart(w http.ResponseWriter, r *http.Request) {
	rv, err := a.loadRide()
	if err != nil || len(rv.Legs) == 0 {
		http.Error(w, "no trip is active", http.StatusConflict)
		return
	}
	st := tripStart(rv.Legs)
	if st != nil && st.ArrivedAt == "" {
		now := time.Now()
		_ = a.openTrip(rv.Ride.Slug, now)
		if err := a.arriveCheckpoint(st, now, nil); err != nil {
			http.Error(w, "could not stamp", http.StatusInternalServerError)
			return
		}
		a.melt.Event("drive_started", map[string]any{"vouched_by": "tap", "km": 0})
		log.Printf("drive started from %s by tap", st.Name)
	}
	http.Redirect(w, r, "/driver", http.StatusSeeOther)
}

// awayFromStart is whether the car has been away: any stop after the start is
// arrived at, or any position stored for the ride since the departure (a
// trail point, or a post's published pin) lies more than checkinRadiusKm from
// the start.
func (a *App) awayFromStart(rideSlug string, legs []Leg) bool {
	st := tripStart(legs)
	if st == nil {
		return true
	}
	for _, c := range rideCheckpoints(legs) {
		if c.ID != st.ID && c.ArrivedAt != "" {
			return true
		}
	}
	since := st.ArrivedAt
	far := func(lat, lng float64, at string) bool {
		return at >= since && haversineKm(lat, lng, st.Lat, st.Lng) > checkinRadiusKm
	}
	if pts, err := a.store.TrailPoints(rideSlug); err == nil {
		for _, t := range pts {
			if far(t.Lat, t.Lng, t.OccurredAt) {
				return true
			}
		}
	}
	if posts, err := a.store.Posts(rideSlug, 0); err == nil {
		for _, p := range posts {
			if p.PinLat != nil && p.PinLng != nil && far(*p.PinLat, *p.PinLng, p.OccurredAt) {
				return true
			}
		}
	}
	return false
}
