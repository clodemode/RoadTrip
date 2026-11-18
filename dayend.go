package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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

// The position rule (2026-11-14). A routing point is passed once a stop after it
// is arrived at or skipped, or once the car's furthest position the app holds
// is at least passedMarginKm beyond it along the day's line. On the day's last
// stretch the only stop after a point is the day's end, so the stamps alone
// never call it passed and the Waze link kept sending the car back (day 4).
//
// Positions are only ones the store already holds, each published when it was
// written (a post's pin, a Chester ask or a track play's trail point): nothing
// raw is read, logged or rendered, and none is kept per tab.
const (
	// passedMarginKm covers a published point's rounding (0.001 degree, about
	// 100 m) and a phone's jitter: a car AT the point has not passed it.
	passedMarginKm = 0.3
	// fixOffRoadKm is how far from the day's line a position can sit and still
	// say how far along the car is; a fix further out (a detour, a bad fix)
	// says nothing, so it drops nothing.
	fixOffRoadKm = 2.0
	// alongTieKm: two places on a line this close to a fix are the same place
	// for the fix (a loop's start and its end), and the earlier one is taken.
	alongTieKm = 0.25
)

// alongFrom is alongLine restricted to the part of the line from fromKm on, and
// for a fix near two places on the line (a loop that ends where it began, a road
// that crosses itself) it takes the earlier, so a car just leaving the start of
// a loop is never read as at its end.
func alongFrom(line json.RawMessage, lat, lng, fromKm float64) (alongKm, offKm float64, ok bool) {
	var pts [][]float64
	if err := json.Unmarshal(line, &pts); err != nil || len(pts) < 2 {
		return 0, 0, false
	}
	offKm = math.Inf(1)
	cum := 0.0
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		if len(a) < 2 || len(b) < 2 {
			return 0, 0, false
		}
		seg := haversineKm(a[1], a[0], b[1], b[0])
		if cum+seg >= fromKm {
			kx := math.Cos((a[1] + b[1]) / 2 * math.Pi / 180)
			dx, dy := (b[0]-a[0])*kx, b[1]-a[1]
			px, py := (lng-a[0])*kx, lat-a[1]
			t := 0.0
			if d := dx*dx + dy*dy; d > 0 {
				t = math.Max(0, math.Min(1, (px*dx+py*dy)/d))
			}
			off := haversineKm(lat, lng, a[1]+t*(b[1]-a[1]), a[0]+t*(b[0]-a[0]))
			if off < offKm-alongTieKm {
				offKm, alongKm = off, cum+t*seg
			}
		}
		cum += seg
	}
	return alongKm, offKm, !math.IsInf(offKm, 1)
}

// carProgress is how far along the leg's line the car has got, by the furthest
// of the positions the app holds for this leg, or false when it holds none that
// sit on the line. Only fixes from the latest passed stop on are read, so a
// stretch already driven is never re-read. Furthest, not latest: a noisy fix
// behind the car cannot bring a point it has passed back.
func carProgress(leg *Leg, held []Post) (float64, bool) {
	from := 0.0
	if p := latestPassedOrd(leg); p > 0 {
		for _, c := range leg.Checkpoints {
			if c.Ord == p {
				if a, _, _, ok := alongLine(leg.Line, c.Lat, c.Lng); ok {
					from = math.Max(0, a-passedMarginKm)
				}
			}
		}
	}
	best, have := 0.0, false
	for _, p := range held {
		if p.LegID != leg.ID || p.PinLat == nil || p.PinLng == nil {
			continue
		}
		if a, off, ok := alongFrom(leg.Line, *p.PinLat, *p.PinLng, from); ok && off <= fixOffRoadKm && (!have || a > best) {
			best, have = a, true
		}
	}
	return best, have
}

// routingAhead is the leg's routing points the car has not passed, in order: a
// point is passed once a stop after it is arrived at or skipped (the rule
// nextRoutingPoint applies), or once the car's furthest held position is
// passedMarginKm beyond it. held is the ride's posts and trail points; none
// held means the stamps alone decide, as before.
func routingAhead(leg *Leg, held []Post) []RoutingPoint {
	along, haveAlong := carProgress(leg, held)
	var out []RoutingPoint
	for _, rp := range leg.RoutingPoints {
		passed := false
		for _, c := range leg.Checkpoints {
			if c.Ord > rp.AfterOrd && (c.ArrivedAt != "" || c.SkippedAt != "") {
				passed = true
			}
		}
		if !passed && haveAlong {
			if a, _, _, ok := alongLine(leg.Line, rp.Lat, rp.Lng); ok && along >= a+passedMarginKm {
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
// nil when the leg's last stop is settled, or is the trip's start. held is the
// ride's posts and trail points, the positions a routing point is passed by.
func dayEnd(legs []Leg, leg *Leg, multi bool, addr map[int]string, held []Post) *dayEndView {
	if leg == nil {
		return nil
	}
	term := legTerminus(*leg)
	if term == nil || term.IsStart || term.ArrivedAt != "" || term.SkippedAt != "" {
		return nil
	}
	v := &dayEndView{LegSlug: leg.Slug, LegOrd: leg.Ord, Multi: multi, Name: term.Name}
	v.nearLat, v.nearLng = lineMiddle(leg.Line)
	// Every navigator goes to the stop's address when the trip-keeper has
	// one (stopNav); else a private place goes by its name, never the cell,
	// and any other stop by its point.
	destPt := ""
	destText := stopNavText(*term, addr)
	dest := stopNav(*term, addr)
	if term.Precision != precisionFuzzed && addr[term.Ord] == "" {
		destPt = pointText(term.Lat, term.Lng)
	}
	v.Waze, v.Google, v.Apple = dest.Waze, dest.Google, dest.Apple
	if ahead := routingAhead(leg, held); len(ahead) > 0 {
		first := mapLinks(ahead[0].Lat, ahead[0].Lng, ahead[0].Name)
		v.Waze, v.Apple, v.ViaFirst = first.Waze, first.Apple, ahead[0].Name
		var wps []string
		for _, rp := range ahead {
			v.Via = append(v.Via, rp.Name)
			wps = append(wps, pointText(rp.Lat, rp.Lng))
		}
		v.Google = googleDirVia(destPt, destText, wps)
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
