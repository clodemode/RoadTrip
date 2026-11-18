package main

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Drop Pin (Kris, 2026-11-18): one button in the header of every page, for
// anyone signed in, that stores where the phone is as a dot on the map. Ruled
// the same morning: a dot only, never a post. It is a trail point with source
// "pin" (trail_points.go), so it goes through publishPosition like every
// other point, and it never checks in at a stop or skips one.

// pinJS is one dropped pin on the map: a published point and its hour.
type pinJS struct {
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
	When string  `json:"when"`
}

// dropPin is POST /driver/pin: lat and lng from the phone.
func (a *App) dropPin(w http.ResponseWriter, r *http.Request) {
	lat, errLat := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue("lat")), 64)
	lng, errLng := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue("lng")), 64)
	if errLat != nil || errLng != nil || math.IsNaN(lat) || math.IsNaN(lng) ||
		lat < -90 || lat > 90 || lng < -180 || lng > 180 || (lat == 0 && lng == 0) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "refused", "error": "no position from the phone"})
		return
	}
	if !a.dropTrailPoint(lat, lng, trailSourcePin, time.Now()) {
		writeJSON(w, http.StatusConflict, map[string]any{"status": "refused", "error": "no trip is under way to pin it on"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "dropped"})
}

// mapPins is a ride's dropped pins for the map, those on legs when legs is
// given (a leg page), oldest first. A store error draws no pins.
func (a *App) mapPins(rideSlug string, legs []Leg) []pinJS {
	tps, err := a.store.TrailPoints(rideSlug)
	if err != nil {
		return []pinJS{}
	}
	on := map[int64]bool{}
	for _, l := range legs {
		on[l.ID] = true
	}
	out := []pinJS{}
	for _, tp := range tps {
		if tp.Source != trailSourcePin || (len(on) > 0 && !on[tp.LegID]) {
			continue
		}
		out = append(out, pinJS{Lat: tp.Lat, Lng: tp.Lng, When: localFmt(tp.OccurredAt, tp.TZ, fmtClock)})
	}
	return out
}
