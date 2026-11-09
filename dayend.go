package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The day's end is the first thing the Ride page offers to navigate to (Kris,
// 2026-11-09): the last stop of the leg being driven, in Waze, Google Maps and
// Apple Maps, with Add a stop beside it. An added stop is a routing point
// (routing.go): a place to drive through, never a check-in. A Waze or Apple
// Maps link holds one destination, so those two go to the first stop still
// ahead and move on once the car has passed it; Google Maps is the one
// navigator whose link carries waypoints, so it gets the day's end with every
// stop ahead on the way. The page says which is which, so nobody believes a
// stop reached a navigator that dropped it.

type dayEndView struct {
	LegSlug string
	LegOrd  int
	Multi   bool
	Name    string // the day's last stop
	Waze    string
	Google  string
	Apple   string
	// ViaFirst is the stop Waze and Apple Maps go to before the day's end;
	// Via is every stop still ahead, in order. Both empty when none.
	ViaFirst string
	Via      []string
	// After is the stop an added place follows: the latest stop passed on
	// this leg, else where the leg begins.
	After   int
	CanAdd  bool // the place search is configured
	Query   string
	Found   *foundPlace
	FindErr string
	// nearLat/nearLng bias the place search to the day's road: the middle
	// vertex of the leg's planned line, a planning-grade public point that is
	// never a private place (the line is masked around those). Zero when the
	// leg has no line.
	nearLat, nearLng float64
}

// lineMiddle is the middle of a leg's planned line: its middle vertex, or
// with an even count the midpoint of the two middle vertices, which stays on
// the line. Zero when the leg has no line.
func lineMiddle(line json.RawMessage) (lat, lng float64) {
	var pts [][]float64
	if json.Unmarshal(line, &pts) != nil || len(pts) == 0 {
		return 0, 0
	}
	a, b := pts[len(pts)/2], pts[len(pts)/2]
	if len(pts)%2 == 0 {
		a = pts[len(pts)/2-1]
	}
	if len(a) < 2 || len(b) < 2 {
		return 0, 0
	}
	return (a[1] + b[1]) / 2, (a[0] + b[0]) / 2
}

// foundPlace is what the place search answered, shown once for a look before
// it is added. The coordinate rides in the Add form only.
type foundPlace struct {
	Label    string
	Lat, Lng float64
}

// routingAhead is the leg's routing points the car has not passed, in order:
// a point is passed once a stop after it is arrived at or skipped, the same
// rule nextRoutingPoint applies when it has no position.
func routingAhead(leg *Leg) []RoutingPoint {
	var out []RoutingPoint
	for _, rp := range leg.RoutingPoints {
		passed := false
		for _, c := range leg.Checkpoints {
			if c.Ord > rp.AfterOrd && (c.ArrivedAt != "" || c.SkippedAt != "") {
				passed = true
			}
		}
		if !passed {
			out = append(out, rp)
		}
	}
	return out
}

func pointText(lat, lng float64) string {
	return strconv.FormatFloat(lat, 'f', -1, 64) + "," + strconv.FormatFloat(lng, 'f', -1, 64)
}

// googleDirVia is the Google Maps directions link to a destination by way of
// stops. A private destination is its name, never its cell.
func googleDirVia(destPt, name string, via []string) string {
	q := url.Values{"api": {"1"}, "destination": {destPt}}
	if destPt == "" {
		q.Set("destination", name)
	}
	if len(via) > 0 {
		q.Set("waypoints", strings.Join(via, "|"))
	}
	return "https://www.google.com/maps/dir/?" + q.Encode()
}

// dayEnd builds the block for the leg being driven (the page's default leg).
// nil when the leg's last stop is settled, or is the trip's start.
func dayEnd(legs []Leg, leg *Leg, multi bool) *dayEndView {
	if leg == nil {
		return nil
	}
	term := legTerminus(*leg)
	if term == nil || term.IsStart || term.ArrivedAt != "" || term.SkippedAt != "" {
		return nil
	}
	v := &dayEndView{LegSlug: leg.Slug, LegOrd: leg.Ord, Multi: multi, Name: term.Name}
	v.nearLat, v.nearLng = lineMiddle(leg.Line)
	// A private place is a snapped cell; every navigator gets its name, never the cell.
	destPt := ""
	dest := mapLinks(0, 0, term.Name)
	if term.Precision != precisionFuzzed {
		dest = mapLinks(term.Lat, term.Lng, term.Name)
		destPt = pointText(term.Lat, term.Lng)
	}
	v.Waze, v.Google, v.Apple = dest.Waze, dest.Google, dest.Apple
	if ahead := routingAhead(leg); len(ahead) > 0 {
		first := mapLinks(ahead[0].Lat, ahead[0].Lng, ahead[0].Name)
		v.Waze, v.Apple, v.ViaFirst = first.Waze, first.Apple, ahead[0].Name
		var wps []string
		for _, rp := range ahead {
			v.Via = append(v.Via, rp.Name)
			wps = append(wps, pointText(rp.Lat, rp.Lng))
		}
		v.Google = googleDirVia(destPt, term.Name, wps)
	}
	byLeg := map[int][]int{}
	for i := range legs {
		for _, c := range legs[i].Checkpoints {
			byLeg[legs[i].Ord] = append(byLeg[legs[i].Ord], c.Ord)
		}
	}
	lo, _, _ := routingRange(byLeg, leg.Ord)
	v.After = lo
	if p := latestPassedOrd(leg); p > lo {
		v.After = p
	}
	return v
}

// lookupStop asks the place search for the one place the trip-keeper typed,
// for a look before it is added. The result is shown by name; the error names
// no URL and no key (freeWhy).
func (a *App) lookupStop(r *http.Request, de *dayEndView) {
	de.Query = cleanLine(r.URL.Query().Get("stop"), freeMaxDest)
	if de.Query == "" {
		return
	}
	if !de.CanAdd {
		de.FindErr = "Add a stop needs the place search, which is not configured here."
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), freeTimeout)
	defer cancel()
	st, err := a.freeSvc().geocodeNear(ctx, a.userOf(r), de.Query, de.nearLat, de.nearLng)
	switch {
	case errors.Is(err, errNoPlace):
		de.FindErr = "No place found for " + de.Query + "."
	case err != nil:
		de.FindErr = "The place search did not answer: " + freeWhy(err)
	default:
		de.Found = &foundPlace{Label: st.Label, Lat: st.Lat, Lng: st.Lng}
	}
}
