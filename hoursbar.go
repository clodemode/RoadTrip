package main

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// The hours bar: one day's drive as a row of whole hours. It starts at the
// day's origin at h0, and each whole hour after that is labelled with the
// place the car should be at by then at the trip's average speed along the
// planned line; the last tick is the finish, "about h2¾".
//
// Privacy: a tick carries an hour and, when a known place lies near that
// point of the line, only that place's name. The point itself is never put on
// a page or in JSON (CLAUDE.md rule 4), and a fuzzed checkpoint never names
// an hour: its name would say where a private place is.

// defaultMPH is the speed when the trip document gives none.
const defaultMPH = 60.0

// hourNameKm is how near a known place must be to name an hour.
const hourNameKm = 15.0

type hourTick struct {
	Label string // "h1", or "about h2¾" for the finish
	Name  string // "" when no known place is near
	At    int    // percent along the bar
}

// avgMPH reads the trip document's avg_mph and nothing else from it.
func (a *App) avgMPH(slug string) float64 {
	raw, err := readTrip(a.dataDir, slug)
	if err != nil {
		return defaultMPH
	}
	var d struct {
		AvgMPH float64 `json:"avg_mph"`
	}
	if json.Unmarshal(raw, &d) != nil || d.AvgMPH <= 0 {
		return defaultMPH
	}
	return d.AvgMPH
}

// linePoints reads a leg's GeoJSON line as [lat,lng] pairs.
func linePoints(line json.RawMessage) [][2]float64 {
	// The store keeps a leg's line as the bare coordinate array; a whole
	// LineString object is read too.
	var coords [][]float64
	if json.Unmarshal(line, &coords) != nil {
		var g struct {
			Coordinates [][]float64 `json:"coordinates"`
		}
		if json.Unmarshal(line, &g) != nil {
			return nil
		}
		coords = g.Coordinates
	}
	pts := make([][2]float64, 0, len(coords))
	for _, c := range coords {
		if len(c) >= 2 {
			pts = append(pts, [2]float64{c[1], c[0]})
		}
	}
	return pts
}

// quarterHours renders hours to the nearest quarter: 2.75 is "2¾".
func quarterHours(h float64) string {
	q := int(math.Round(h * 4))
	whole, frac := q/4, q%4
	s := ""
	if whole > 0 || frac == 0 {
		s = strconv.Itoa(whole)
	}
	return s + [...]string{"", "¼", "½", "¾"}[frac]
}

// hourPoint is one whole hour's place on the map: the point ON the leg's
// planned line at that hour's distance. The planned line is already
// published, so the point is no more private than the line. It is never a
// raw fix or a private-file value.
type hourPoint struct {
	H     int     `json:"h"`
	Label string  `json:"label"`
	Name  string  `json:"name"`
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
}

// hoursResult is one leg's bar: the text ticks, the points for the map, and
// the stretch of planned line the bar measures (lng,lat pairs, like Line).
type hoursResult struct {
	Ticks   []hourTick
	Points  []hourPoint
	Stretch [][]float64
}

// projectOnLine returns how far along pts (in miles) the point nearest
// (lat,lng) lies, and how far (miles) that point is from (lat,lng).
func projectOnLine(pts [][2]float64, lat, lng float64) (along, off float64) {
	best, cum := math.Inf(1), 0.0
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		seg := simHaversine(a[0], a[1], b[0], b[1])
		k := math.Cos(a[0] * math.Pi / 180)
		bx, by := (b[1]-a[1])*k, b[0]-a[0]
		px, py := (lng-a[1])*k, lat-a[0]
		t := 0.0
		if l2 := bx*bx + by*by; l2 > 0 {
			t = math.Max(0, math.Min(1, (px*bx+py*by)/l2))
		}
		q := [2]float64{a[0] + t*(b[0]-a[0]), a[1] + t*(b[1]-a[1])}
		if d := simHaversine(q[0], q[1], lat, lng); d < best {
			best, along = d, cum+t*seg
		}
		cum += seg
	}
	return along, best
}

// maxAnchorMi is how far from the planned line an arrived stop may sit and
// still re-anchor the bar.
const maxAnchorMi = 20.0

// hoursBar builds one leg's bar. origin and terminus are the leg's end
// checkpoints (nil when it has none); anchor, when given, is the latest
// arrived checkpoint on the leg and h0 is where it lies on the line: the bar
// measures what is left. known are the named things an hour may take its name
// from.
func hoursBar(line json.RawMessage, mph float64, origin, anchor, terminus *Checkpoint, known []namedPoint) hoursResult {
	pts := linePoints(line)
	if len(pts) < 2 || mph <= 0 {
		return hoursResult{}
	}
	s0, h0 := 0.0, origin
	if anchor != nil {
		if s, off := projectOnLine(pts, anchor.Lat, anchor.Lng); off <= maxAnchorMi {
			s0, h0 = s, anchor
		}
	}
	total := simPathLength(pts)
	hours := (total - s0) / mph
	name := func(c *Checkpoint) string {
		if c == nil || c.Precision == precisionFuzzed {
			return ""
		}
		return c.Name
	}
	pct := func(h float64) int { return int(math.Round(100 * math.Min(1, h/hours))) }
	res := hoursResult{Ticks: []hourTick{{Label: "h0", Name: name(h0), At: 0}}}
	at := func(h int) [2]float64 { p, _ := simWalkPath(pts, s0+float64(h)*mph); return p }
	p0 := at(0)
	res.Points = append(res.Points, hourPoint{H: 0, Label: "h0", Name: name(h0), Lat: p0[0], Lng: p0[1]})
	for h := 1; float64(h) <= hours-0.125; h++ {
		p := at(h)
		n := nearestName(p, known)
		res.Ticks = append(res.Ticks, hourTick{Label: "h" + strconv.Itoa(h), Name: n, At: pct(float64(h))})
		res.Points = append(res.Points, hourPoint{H: h, Label: "h" + strconv.Itoa(h), Name: n, Lat: p[0], Lng: p[1]})
	}
	res.Ticks = append(res.Ticks, hourTick{Label: "about h" + quarterHours(hours), Name: name(terminus), At: 100})
	res.Stretch = append(res.Stretch, []float64{p0[1], p0[0]})
	cum := 0.0
	for i := 1; i < len(pts); i++ {
		cum += simHaversine(pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1])
		if cum > s0+1e-9 {
			res.Stretch = append(res.Stretch, []float64{pts[i][1], pts[i][0]})
		}
	}
	return res
}

type namedPoint struct {
	Name     string
	Lat, Lng float64
}

// nearestName is the name of the known point nearest p, within hourNameKm.
func nearestName(p [2]float64, known []namedPoint) string {
	best, bestKm := "", hourNameKm
	for _, k := range known {
		if d := simHaversine(p[0], p[1], k.Lat, k.Lng) * 1.609344; d <= bestKm {
			best, bestKm = k.Name, d
		}
	}
	return best
}

// knownPlaces is every point an hour may be named after: the ride's checkpoints
// that are not private, its sights, and its places.
func knownPlaces(legs []Leg, places []Place) []namedPoint {
	return knownMilestones(legs, places, nil)
}

// knownMilestones is knownPlaces with the ride's routing points too: an hour is
// named for the nearest stop or routing point, whichever is nearer. A routing
// point is a place to drive through, never a stop, and it is only a name here.
// Nothing sitting in a private place's zone, or carrying a private place's
// name, names an hour, whatever kind of place it is: a routing point is
// checked when it is written, and again here on the way out.
func knownMilestones(legs []Leg, places []Place, routing []RoutingPoint) []namedPoint {
	var out []namedPoint
	for _, r := range routing {
		if strings.TrimSpace(r.Name) != "" {
			out = append(out, namedPoint{r.Name, r.Lat, r.Lng})
		}
	}
	for _, l := range legs {
		for _, c := range l.Checkpoints {
			if c.Precision != precisionFuzzed && strings.TrimSpace(c.Name) != "" {
				out = append(out, namedPoint{c.Name, c.Lat, c.Lng})
			}
		}
		for _, s := range l.Sights {
			if strings.TrimSpace(s.Name) != "" {
				out = append(out, namedPoint{s.Name, s.Lat, s.Lng})
			}
		}
	}
	for _, p := range places {
		out = append(out, namedPoint{p.Name, p.Lat, p.Lng})
	}
	privates := fuzzedOf(rideCheckpoints(legs))
	kept := out[:0]
	for _, k := range out {
		if !inPrivateZoneOf(k.Lat, k.Lng, k.Name, privates) {
			kept = append(kept, k)
		}
	}
	return kept
}

// legBar is one leg's hours bar as a page draws it.
type legBar struct {
	Leg   *Leg
	Ticks []hourTick
	res   hoursResult
}

// hoursView is what the hours_bar template is called with. Multi says to
// title each bar with its day chip.
type hoursView struct {
	Bars  []legBar
	Multi bool
}

// latestArrived is the last checkpoint, in route order, the car has arrived
// at on leg i, counting the leg's origin (the boundary stop belongs to the
// earlier leg). ArrivedAt is empty for an un-arrived and a skipped stop alike,
// so neither anchors the bar.
func latestArrived(legs []Leg, i int) *Checkpoint {
	var out *Checkpoint
	if o := legOrigin(legs, i); o != nil && o.ArrivedAt != "" {
		out = o
	}
	for j := range legs[i].Checkpoints {
		if c := &legs[i].Checkpoints[j]; c.ArrivedAt != "" {
			out = c
		}
	}
	return out
}

// hoursBars builds the bar for each of the given legs (indexes into all). A
// leg whose end the car has reached draws none; every other leg is measured
// forward from the latest arrived stop on it, or from its origin.
func (a *App) hoursBars(slug string, all []Leg, which []int, multi bool) hoursView {
	places, _ := a.store.Places(slug)
	var routing []RoutingPoint
	for _, l := range all {
		routing = append(routing, l.RoutingPoints...)
	}
	known := knownMilestones(all, places, routing)
	mph := a.avgMPH(slug)
	out := hoursView{Multi: multi}
	for _, i := range which {
		term := legTerminus(all[i])
		if term != nil && term.ArrivedAt != "" {
			continue
		}
		r := hoursBar(all[i].Line, mph, legOrigin(all, i), latestArrived(all, i), term, known)
		if len(r.Ticks) > 0 {
			out.Bars = append(out.Bars, legBar{Leg: &all[i], Ticks: r.Ticks, res: r})
		}
	}
	return out
}

// withHours puts each bar's points and stretch on the map payload.
func withHours(m mapJS, v hoursView) mapJS {
	for _, b := range v.Bars {
		for i := range m.Legs {
			if m.Legs[i].ID == b.Leg.ID {
				m.Legs[i].Hours, m.Legs[i].HoursLine = b.res.Points, b.res.Stretch
			}
		}
	}
	return m
}

func allIndexes(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// legIndex is where leg sits in legs (by id), or 0.
func legIndex(legs []Leg, leg *Leg) int {
	for i := range legs {
		if legs[i].ID == leg.ID {
			return i
		}
	}
	return 0
}
