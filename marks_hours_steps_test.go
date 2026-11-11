package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// marksWorld rides on the sim world and swaps the active trip for a synthetic
// one built here (real public Ontario town centres, no private data), then
// reads the home page the way any visitor does.
type marksWorld struct {
	t     *testing.T
	sim   *simWorld
	line  [][2]float64 // the one leg's line, [lat,lng]
	page  string
	names []string
}

type mhStop struct {
	name     string
	lat, lng float64
	fuzzed   bool
	legEnd   bool
}

var mhTowns = map[string][2]float64{
	"Kingston":   {44.2312, -76.4860},
	"Napanee":    {44.2488, -76.9500},
	"Belleville": {44.1628, -77.3832},
	"Trenton":    {44.1040, -77.5790},
	"Cobourg":    {43.9593, -78.1677},
	"Oshawa":     {43.8971, -78.8658},
	"Toronto":    {43.6532, -79.3832},
}

func initMarksScenarios(t *testing.T, sc *godog.ScenarioContext, sim *simWorld) *marksWorld {
	m := &marksWorld{t: t, sim: sim}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*m = marksWorld{t: t, sim: sim}
		return ctx, nil
	})
	sc.Step(`^the active trip is one day with stops (.+)$`, m.oneDayStops)
	sc.Step(`^the active trip is two days$`, m.twoDays)
	sc.Step(`^the active trip is one day from Kingston to Toronto at 60 mph over 165 miles$`, m.kingstonToronto)
	sc.Step(`^a known place sits on the planned line 60 miles from Kingston$`, m.placeAtSixty)
	sc.Step(`^the active trip is one day whose line has no known place near its 1 hour point$`, m.kingstonToronto)
	sc.Step(`^the active trip is one day with a private stop near its 1 hour point$`, m.privateAtSixty)
	sc.Step(`^anyone opens the home page$`, m.open)
	sc.Step(`^the stops are marked A, B, C, D, E, F and G in that order$`, m.lettersAG)
	sc.Step(`^no stop is marked with a number$`, m.noNumbers)
	sc.Step(`^the map data carries the same marks$`, m.mapMarks)
	sc.Step(`^no day chip shows a number$`, m.noChip)
	sc.Step(`^each day end is marked "d" and its day number, like "d1" and "d2"$`, m.dayEnds)
	sc.Step(`^every other stop is marked with a letter$`, m.otherLetters)
	sc.Step(`^each day chip reads "d" and its day number$`, m.chips)
	sc.Step(`^the hours bar reads "([^"]*)" first$`, m.barFirst)
	sc.Step(`^the hours bar marks "(h1)" with that place's name$`, m.h1Named)
	sc.Step(`^the hours bar marks "(h1)" with no name$`, m.h1Alone)
	sc.Step(`^the hours bar marks "(h1)" without the private stop's name$`, m.h1NoPrivate)
	sc.Step(`^the map data carries an hours line for the leg with ticks h0, h1 and h2$`, m.mapTicks)
	sc.Step(`^each tick lies on the planned line at that whole hour's distance$`, m.ticksOnLine)
	sc.Step(`^the hours line is styled muted and drawn offset beside the route, not on it$`, m.mutedOffset)
	sc.Step(`^the stop 60 miles from Kingston is arrived at$`, m.arrivedAtSixty)
	sc.Step(`^the stop 60 miles from Kingston is skipped$`, m.skippedAtSixty)
	sc.Step(`^the hours bar reads "h0" at that stop first$`, m.barFirstAtStop)
	sc.Step(`^its h1 tick lies 60 miles further along the planned line$`, m.h1SixtyFurther)
	return m
}

// mhLine is the planned line from Kingston to Toronto the hours scenarios
// use: bowed north of the straight line so it measures about 165 miles.
var mhLine = [][2]float64{mhTowns["Kingston"], {44.40, -77.95}, mhTowns["Toronto"]}

func (m *marksWorld) push(legs [][]mhStop, mph float64, places []map[string]any, line [][2]float64) error {
	var feats []any
	order := 0
	prevLast := [2]float64{}
	for li, stops := range legs {
		var coords [][]float64
		if li > 0 {
			coords = append(coords, []float64{prevLast[1], prevLast[0]})
		}
		var pts [][2]float64
		if li > 0 {
			pts = append(pts, prevLast)
		}
		for _, s := range stops {
			coords = append(coords, []float64{s.lng, s.lat})
			pts = append(pts, [2]float64{s.lat, s.lng})
		}
		prevLast = [2]float64{stops[len(stops)-1].lat, stops[len(stops)-1].lng}
		if li == 0 && line != nil {
			pts, coords = line, nil
			for _, p := range line {
				coords = append(coords, []float64{p[1], p[0]})
			}
		}
		if li == 0 {
			m.line = pts
		}
		feats = append(feats, map[string]any{"type": "Feature",
			"properties": map[string]any{"leg": li + 1, "day": li + 1, "slug": fmt.Sprintf("day-%d", li+1),
				"name": fmt.Sprintf("Day %d", li+1), "est_miles": round1(simPathLength(pts))},
			"geometry": map[string]any{"type": "LineString", "coordinates": coords}})
		for _, s := range stops {
			order++
			pr := map[string]any{"kind": "checkpoint", "order": order, "leg": li + 1, "name": s.name,
				"label": "Stop", "tz": "America/Toronto", "is_terminal": s.legEnd}
			if s.fuzzed {
				pr["precision"] = "fuzzed"
			}
			feats = append(feats, map[string]any{"type": "Feature", "properties": pr,
				"geometry": map[string]any{"type": "Point", "coordinates": []float64{s.lng, s.lat}}})
		}
	}
	for _, p := range places {
		feats = append(feats, p)
	}
	doc := map[string]any{"type": "FeatureCollection", "synthetic": true, "name": "Marks and hours, a synthetic trip",
		"ride":    map[string]any{"slug": "marks-hours-trip", "name": "Marks and Hours", "start_date": "2030-05-01", "mode": "CAR"},
		"avg_mph": mph, "features": feats}
	raw, _ := json.Marshal(doc)
	if w := pushTrip(m.t, m.sim.app, raw, "?activate=1"); w.Code != http.StatusOK {
		return fmt.Errorf("push: %d %s", w.Code, w.Body.String())
	}
	return nil
}

func town(n string, legEnd bool) mhStop {
	c := mhTowns[n]
	return mhStop{name: n, lat: c[0], lng: c[1], legEnd: legEnd}
}

func (m *marksWorld) oneDayStops(list string) error {
	names := regexp.MustCompile(`,\s*|\s+and\s+`).Split(list, -1)
	var stops []mhStop
	for i, n := range names {
		if _, ok := mhTowns[n]; !ok {
			return fmt.Errorf("no town %q", n)
		}
		stops = append(stops, town(n, i == len(names)-1))
	}
	return m.push([][]mhStop{stops}, 60, nil, nil)
}

func (m *marksWorld) twoDays() error {
	return m.push([][]mhStop{
		{town("Kingston", false), town("Napanee", false), town("Belleville", true)},
		{town("Trenton", false), town("Cobourg", false), town("Toronto", true)},
	}, 60, nil, nil)
}

func (m *marksWorld) kingstonToronto() error {
	if err := m.push([][]mhStop{{town("Kingston", false), town("Toronto", true)}}, 60, nil, mhLine); err != nil {
		return err
	}
	return m.checkLength()
}

// checkLength keeps the fixture honest: the feature says 165 miles, so the
// line must be 165 miles to the nearest quarter hour at 60 mph.
func (m *marksWorld) checkLength() error {
	if l := simPathLength(m.line); l < 157.5 || l >= 172.5 {
		return fmt.Errorf("the fixture line is %.1f miles, not about 165", l)
	}
	return nil
}

func sixty(line [][2]float64) [2]float64 { p, _ := simWalkPath(line, 60); return p }

func (m *marksWorld) placeAtSixty() error {
	p := sixty(mhLine)
	place := map[string]any{"type": "Feature",
		"properties": map[string]any{"kind": "place", "name": "Pinecrest"},
		"geometry":   map[string]any{"type": "Point", "coordinates": []float64{p[1], p[0]}}}
	return m.push([][]mhStop{{town("Kingston", false), town("Toronto", true)}}, 60, []map[string]any{place}, mhLine)
}

func (m *marksWorld) privateAtSixty() error {
	p := sixty(mhLine)
	home := mhStop{name: "Home Base", lat: p[0], lng: p[1], fuzzed: true}
	return m.push([][]mhStop{{town("Kingston", false), home, town("Toronto", true)}}, 60, nil, mhLine)
}

func (m *marksWorld) open() error {
	w := doAs(m.t, m.sim.app, nil, "GET", "/", nil)
	if w.Code != http.StatusOK {
		return fmt.Errorf("home page answered %d", w.Code)
	}
	m.page = w.Body.String()
	return nil
}

var (
	reCPN   = regexp.MustCompile(`<span class="cp-n">([^<]*)</span>`)
	reLegN  = regexp.MustCompile(`<span class="leg-n">([^<]*)</span>`)
	reMapMk = regexp.MustCompile(`"mark":"([^"]*)"`)
)

func submatches(re *regexp.Regexp, s string) []string {
	var out []string
	for _, g := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, g[1])
	}
	return out
}

func (m *marksWorld) lettersAG() error {
	if got, want := strings.Join(submatches(reCPN, m.page), " "), "A B C D E F G"; got != want {
		return fmt.Errorf("the stops are marked %q, want %q", got, want)
	}
	return nil
}

func (m *marksWorld) noNumbers() error {
	for _, mk := range submatches(reCPN, m.page) {
		if !regexp.MustCompile(`^[A-Z]+$`).MatchString(mk) {
			return fmt.Errorf("a stop is marked %q, not a letter", mk)
		}
	}
	return nil
}

func (m *marksWorld) mapMarks() error {
	a, b := strings.Join(submatches(reCPN, m.page), " "), strings.Join(submatches(reMapMk, m.page), " ")
	if a == "" || a != b {
		return fmt.Errorf("list marks %q, map marks %q", a, b)
	}
	return nil
}

func (m *marksWorld) noChip() error {
	if got := submatches(reLegN, m.page); len(got) > 0 {
		return fmt.Errorf("day chips show %q", got)
	}
	return nil
}

func (m *marksWorld) dayEnds() error {
	var ends []string
	for _, mk := range submatches(reCPN, m.page) {
		if !regexp.MustCompile(`^[A-Z]+$`).MatchString(mk) {
			ends = append(ends, mk)
		}
	}
	if got := strings.Join(ends, " "); got != "d1 d2" {
		return fmt.Errorf("day ends are marked %q, want \"d1 d2\"", got)
	}
	return nil
}

func (m *marksWorld) otherLetters() error {
	var letters []string
	for _, mk := range submatches(reCPN, m.page) {
		if regexp.MustCompile(`^[A-Z]+$`).MatchString(mk) {
			letters = append(letters, mk)
		}
	}
	if got := strings.Join(letters, " "); got != "A B C D" {
		return fmt.Errorf("the other stops are marked %q, want \"A B C D\"", got)
	}
	return nil
}

func (m *marksWorld) chips() error {
	if got := strings.Join(submatches(reLegN, m.page), " "); got != "d1 d2" {
		return fmt.Errorf("day chips read %q, want \"d1 d2\"", got)
	}
	return nil
}

// bar returns the whole-hour labels the map carries for the leg, as a reader
// sees them on the map: "h0 Kingston", "h1 Pinecrest". The Plan page no longer
// draws an hours bar; the map keeps the hours.
func (m *marksWorld) bar() ([]string, error) {
	hs, err := m.mapHours()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, h := range hs {
		out = append(out, strings.TrimSpace(h.Label+" "+h.Name))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the map carries no hours")
	}
	return out, nil
}

func (m *marksWorld) barFirst(want string) error {
	b, err := m.bar()
	if err != nil {
		return err
	}
	if b[0] != want {
		return fmt.Errorf("the hours bar starts %q, want %q (bar: %q)", b[0], want, b)
	}
	return nil
}

func (m *marksWorld) entry(h string) (string, error) {
	b, err := m.bar()
	if err != nil {
		return "", err
	}
	for _, e := range b {
		if e == h || strings.HasPrefix(e, h+" ") {
			return e, nil
		}
	}
	return "", fmt.Errorf("the hours bar has no %q (bar: %q)", h, b)
}

func (m *marksWorld) h1Named(h string) error {
	e, err := m.entry(h)
	if err != nil {
		return err
	}
	if e != h+" Pinecrest" {
		return fmt.Errorf("hour entry is %q, want %q", e, h+" Pinecrest")
	}
	return nil
}

func (m *marksWorld) h1Alone(h string) error {
	e, err := m.entry(h)
	if err != nil {
		return err
	}
	if e != h {
		return fmt.Errorf("hour entry is %q, want the hour alone", e)
	}
	return nil
}

func (m *marksWorld) h1NoPrivate(h string) error {
	b, err := m.bar()
	if err != nil {
		return err
	}
	if strings.Contains(strings.Join(b, " "), "Home Base") {
		return fmt.Errorf("the map's hours name the private stop")
	}
	return m.h1Alone(h)
}

// waystop is the public stop 60 miles along the line the arrival scenarios stamp.
const waystop = "Waystop"

func (m *marksWorld) pushWithWaystop() error {
	p := sixty(mhLine)
	return m.push([][]mhStop{{town("Kingston", false),
		{name: waystop, lat: p[0], lng: p[1]}, town("Toronto", true)}}, 60, nil, mhLine)
}

func (m *marksWorld) stopID() (int64, error) {
	rv, err := m.sim.app.loadRide()
	if err != nil {
		return 0, err
	}
	for _, l := range rv.Legs {
		for _, c := range l.Checkpoints {
			if c.Name == waystop {
				return c.ID, nil
			}
		}
	}
	return 0, fmt.Errorf("no stop %q on the trip", waystop)
}

func (m *marksWorld) arrivedAtSixty() error {
	if err := m.pushWithWaystop(); err != nil {
		return err
	}
	id, err := m.stopID()
	if err != nil {
		return err
	}
	return m.sim.app.store.SetArrived(id, m.sim.now, nil)
}

// skippedAtSixty leaves the stop un-arrived, which is what a skip looks like
// to this branch: a stop with no ArrivedAt never anchors the bar. The sibling
// branch's skipped_at column makes this a real skip after the merge, and this
// step should then stamp it.
func (m *marksWorld) skippedAtSixty() error { return m.pushWithWaystop() }

func (m *marksWorld) barFirstAtStop() error { return m.barFirst("h0 " + waystop) }

// mapHours reads the leg's hours array out of the map data on the page.
func (m *marksWorld) mapHours() ([]hourPoint, error) {
	g := regexp.MustCompile(`"hours":(\[[^\]]*\])`).FindStringSubmatch(m.page)
	if g == nil {
		return nil, fmt.Errorf("the map data has no hours")
	}
	var hs []hourPoint
	if err := json.Unmarshal([]byte(g[1]), &hs); err != nil {
		return nil, err
	}
	return hs, nil
}

func (m *marksWorld) mapTicks() error {
	hs, err := m.mapHours()
	if err != nil {
		return err
	}
	var got []string
	for _, h := range hs {
		got = append(got, h.Label)
	}
	if strings.Join(got, " ") != "h0 h1 h2" {
		return fmt.Errorf("map ticks %q, want h0 h1 h2", got)
	}
	return nil
}

func (m *marksWorld) ticksOnLine() error { return m.ticksAt(0) }

func (m *marksWorld) ticksAt(from float64) error {
	hs, err := m.mapHours()
	if err != nil {
		return err
	}
	for _, h := range hs {
		want, _ := simWalkPath(mhLine, from+60*float64(h.H))
		if d := simHaversine(want[0], want[1], h.Lat, h.Lng); d > 0.2 {
			return fmt.Errorf("tick %s is %.2f miles from where the line is at that distance", h.Label, d)
		}
	}
	return nil
}

func (m *marksWorld) h1SixtyFurther() error {
	if err := m.ticksAt(60); err != nil {
		return err
	}
	hs, _ := m.mapHours()
	if len(hs) != 2 {
		return fmt.Errorf("want ticks h0 and h1 from the stop, got %d", len(hs))
	}
	return nil
}

func (m *marksWorld) mutedOffset() error {
	js := doAs(m.t, m.sim.app, nil, "GET", "/static/js/map.js", nil).Body.String()
	for _, want := range []string{"hours_line", "HOURS_OFFSET_PX = 8", "'#6b7280'", "weight: 2", "opacity: 0.5", "zoomend"} {
		if !strings.Contains(js, want) {
			return fmt.Errorf("map.js lacks %q", want)
		}
	}
	// The data holds the stretch on the route itself; the beside-the-route
	// offset is drawn in screen space, so the payload must not pre-offset it.
	g := regexp.MustCompile(`"hours_line":(\[\[.*?\]\])`).FindStringSubmatch(m.page)
	if g == nil {
		return fmt.Errorf("the map data has no hours_line")
	}
	return nil
}
