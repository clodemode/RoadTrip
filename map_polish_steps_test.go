package main

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/ride_map_opens_on_the_whole_trip.feature,
// features/music_notes_show_on_the_ride_map.feature and
// features/listen_inn_panel_on_a_phone.feature. The page's own map.js and
// nowplaying.js run under static/js/map_harness.mjs, which stands in for
// Leaflet and does the real Web Mercator arithmetic for the opening view. It is
// not a browser: paint order, tile loading and popups are not modelled, and the
// CSS is read as text.

type mapPolishWorld struct {
	t       *testing.T
	legs    []map[string]any
	tracks  []map[string]any
	cps     []map[string]any
	plays   []map[string]any
	steps   []map[string]any
	size    map[string]int
	driven  [][2]float64 // lat, lng
	start   [2]float64
	finish  [2]float64
	cpNamed map[string][2]float64
	out     map[string]any
}

// Invented places along an invented road: lat, lng.
var (
	mpStart  = [2]float64{36.1, -86.8}
	mpDay1   = [2]float64{34.0, -90.0}
	mpMid    = [2]float64{32.5, -94.5}
	mpFinish = [2]float64{30.4, -97.9}
)

func initMapPolishScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &mapPolishWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = mapPolishWorld{t: t, cpNamed: map[string][2]float64{}}
		return ctx, nil
	})
	line := func(a ...[2]float64) [][]float64 {
		var o [][]float64
		for _, p := range a {
			o = append(o, []float64{p[1], p[0]})
		}
		return o
	}
	sc.Step(`^a ride of three days from "([^"]*)" to "([^"]*)" where the first two days are driven$`, func(from, to string) error {
		w.legs = []map[string]any{
			{"ord": 1, "slug": "d1", "name": "Day 1", "date": "2026-11-10", "color": "#0d6efd", "line": line(mpStart, mpDay1)},
			{"ord": 2, "slug": "d2", "name": "Day 2", "date": "2026-11-11", "color": "#0d6efd", "line": line(mpDay1, mpMid)},
			{"ord": 3, "slug": "d3", "name": "Day 3", "date": "2026-11-12", "color": "#0d6efd", "line": line(mpMid, mpFinish)},
		}
		w.tracks = []map[string]any{
			{"leg": 1, "slug": "d1", "name": "Day 1", "color": "#0d6efd", "line": line(mpStart, mpDay1)},
			{"leg": 2, "slug": "d2", "name": "Day 2", "color": "#0d6efd", "line": line(mpDay1, mpMid)},
		}
		cp := func(ord int, name, mark, leg string, p [2]float64, isStart bool) map[string]any {
			w.cpNamed[name] = p
			return map[string]any{"ord": ord, "name": name, "mark": mark, "lat": p[0], "lng": p[1], "tz": "America/Chicago", "leg_slug": leg, "is_start": isStart}
		}
		w.cps = []map[string]any{cp(1, from, "d0", "d1", mpStart, true), cp(2, "Day One Town", "d1", "d1", mpDay1, false),
			cp(3, "Mid Town", "d2", "d2", mpMid, false), cp(4, to, "d3", "d3", mpFinish, false)}
		w.start, w.finish = mpStart, mpFinish
		w.driven = [][2]float64{mpStart, mpDay1, mpMid}
		return nil
	})
	sc.Step(`^the map's box measures (\d+) by (\d+) when the page first runs$`, func(a, b string) error {
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		w.size = map[string]int{"w": x, "h": y}
		return nil
	})
	add := func(m map[string]any) { w.steps = append(w.steps, m) }
	sc.Step(`^a finger touches the map while the page settles$`, func() error { add(map[string]any{"do": "touch"}); return nil })
	sc.Step(`^the wheel turns over the map while the page settles$`, func() error { add(map[string]any{"do": "wheel"}); return nil })
	sc.Step(`^the visitor drags the map$`, func() error { add(map[string]any{"do": "drag"}); return nil })
	sc.Step(`^the page finishes laying out at (\d+) by (\d+)$`, func(a, b string) error {
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		add(map[string]any{"do": "resize", "w": x, "h": y})
		return nil
	})
	probe := func(p [2]float64) error {
		o, err := w.run([][2]float64{p}, nil)
		if err != nil {
			return err
		}
		if c := o["contains"].([]any); len(c) != 1 || c[0] != true {
			return fmt.Errorf("the point is outside the opening view (zoom %v)", o["zoom"])
		}
		return nil
	}
	sc.Step(`^the destination is inside the opening view$`, func() error { return probe(w.finish) })
	sc.Step(`^the start is inside the opening view$`, func() error { return probe(w.start) })
	sc.Step(`^the driven part is inside the opening view$`, func() error {
		for _, p := range w.driven {
			if err := probe(p); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the zoom is the one the visitor had$`, func() error {
		after, err := w.run(nil, nil)
		if err != nil {
			return err
		}
		var kept []map[string]any
		for _, s := range w.steps {
			if s["do"] != "resize" {
				kept = append(kept, s)
			}
		}
		before, err := w.run(nil, kept)
		if err != nil {
			return err
		}
		if after["zoom"] != before["zoom"] || after["fits"] != before["fits"] {
			return fmt.Errorf("the resize re-fitted the map: zoom %v -> %v, fits %v -> %v", before["zoom"], after["zoom"], before["fits"], after["fits"])
		}
		return nil
	})

	// --- music notes
	sc.Step(`^three tracks played along the first two days$`, func() error {
		for i, p := range [][2]float64{{35.5, -87.5}, {33.0, -92.0}, {33.7, -90.4}} {
			w.plays = append(w.plays, map[string]any{"id": fmt.Sprintf("p%d", i), "track": fmt.Sprintf("Song %d", i), "artist": "A", "album": "Album",
				"started_at": "2026-11-12T12:00:00Z", "lat": p[0], "lng": p[1]})
		}
		return nil
	})
	sc.Step(`^a track started at the stop "([^"]*)"$`, func(name string) error {
		p := w.cpNamed[name]
		w.plays = append(w.plays, map[string]any{"id": "pstop", "track": "At the stop", "artist": "A", "album": "Album",
			"started_at": "2026-11-12T12:00:00Z", "lat": p[0], "lng": p[1]})
		return nil
	})
	sc.Step(`^a track played with no point$`, func() error {
		w.plays = append(w.plays, map[string]any{"id": "pnone", "track": "Nowhere", "artist": "A", "album": "Album", "started_at": "2026-11-12T12:00:00Z"})
		return nil
	})
	notes := func(o map[string]any) []map[string]any {
		var r []map[string]any
		for _, m := range o["markers"].([]any) {
			mm := m.(map[string]any)
			if strings.Contains(fmt.Sprint(mm["cls"]), "np-pin") {
				r = append(r, mm)
			}
		}
		return r
	}
	sc.Step(`^every music note is inside the opening view$`, func() error {
		o, err := w.run(nil, nil)
		if err != nil {
			return err
		}
		ns := notes(o)
		if len(ns) == 0 {
			return fmt.Errorf("no music note was drawn")
		}
		for _, n := range ns {
			if n["inView"] != true {
				return fmt.Errorf("a music note is outside the opening view (zoom %v)", o["zoom"])
			}
		}
		return nil
	})
	sc.Step(`^every music note is at least (\d+) pixels across$`, func(min string) error {
		want, _ := strconv.Atoi(min)
		o, err := w.run(nil, nil)
		if err != nil {
			return err
		}
		ns := notes(o)
		if len(ns) == 0 {
			return fmt.Errorf("no music note was drawn")
		}
		for _, n := range ns {
			sz := n["iconSize"].([]any)
			if int(sz[0].(float64)) < want || int(sz[1].(float64)) < want {
				return fmt.Errorf("a music note is %vx%v pixels", sz[0], sz[1])
			}
		}
		return nil
	})
	sc.Step(`^the music note at "([^"]*)" is stacked above the stop pins$`, func(name string) error {
		p := w.cpNamed[name]
		o, err := w.run(nil, nil)
		if err != nil {
			return err
		}
		top, found := math.Inf(-1), false
		for _, m := range o["markers"].([]any) {
			mm := m.(map[string]any)
			if mm["lat"] == p[0] && mm["lng"] == p[1] && !strings.Contains(fmt.Sprint(mm["cls"]), "np-pin") {
				top = math.Max(top, mm["z"].(float64))
			}
		}
		for _, n := range notes(o) {
			if n["lat"] == p[0] && n["lng"] == p[1] {
				found = true
				if n["z"].(float64) <= top {
					return fmt.Errorf("the note's stacking %v is not above the stop pin's %v: a tap lands on the pin", n["z"], top)
				}
			}
		}
		if !found {
			return fmt.Errorf("no music note at %q", name)
		}
		return nil
	})
	sc.Step(`^(\d+) music notes are drawn$`, func(n string) error {
		want, _ := strconv.Atoi(n)
		o, err := w.run(nil, nil)
		if err != nil {
			return err
		}
		if got := len(notes(o)); got != want {
			return fmt.Errorf("%d music notes drawn, want %d", got, want)
		}
		return nil
	})

	// --- the list
	sc.Step(`^Listen Inn lists the tracks "([^"]*)" and "([^"]*)" of album "([^"]*)" by "([^"]*)" with the blurb "([^"]*)"$`, func(a, b, album, artist, blurb string) error {
		w.addTrack(a, album, artist, blurb)
		w.addTrack(b, album, artist, blurb)
		return nil
	})
	sc.Step(`^Listen Inn also lists the track "([^"]*)" of album "([^"]*)" by "([^"]*)" with the blurb "([^"]*)"$`, func(a, album, artist, blurb string) error {
		w.addTrack(a, album, artist, blurb)
		return nil
	})
	sc.Step(`^Listen Inn lists the track "([^"]*)" of album "([^"]*)" by "([^"]*)" with the blurb "([^"]*)"$`, func(a, album, artist, blurb string) error {
		w.addTrack(a, album, artist, blurb)
		return nil
	})
	sc.Step(`^Listen Inn lists the track "([^"]*)" of album "([^"]*)" by "([^"]*)" with a blurb of (\d+) words$`, func(a, album, artist, n string) error {
		c, _ := strconv.Atoi(n)
		var ws []string
		for i := 1; i <= c; i++ {
			ws = append(ws, fmt.Sprintf("word%02d", i))
		}
		w.addTrack(a, album, artist, strings.Join(ws, " "))
		return nil
	})
	sc.Step(`^the song "([^"]*)" played before the song "([^"]*)"$`, func(first, second string) error {
		// Newest first, as /tracks.json sends them.
		w.plays = []map[string]any{
			{"id": "t2", "track": second, "artist": "Other Band", "album": "Red Album", "started_at": "2026-11-12T12:05:00Z"},
			{"id": "t1", "track": first, "artist": "Some Band", "album": "Blue Album", "started_at": "2026-11-12T12:00:00Z"}}
		return nil
	})
	panel := func() (string, error) {
		w.size = map[string]int{"w": 375, "h": 380}
		w.legs, w.cps, w.tracks = nil, nil, nil
		o, err := w.run(nil, nil)
		if err != nil {
			return "", err
		}
		return o["panel"].(string), nil
	}
	sc.Step(`^the list shows the blurb "([^"]*)" (\d+) times?$`, func(blurb, n string) error {
		want, _ := strconv.Atoi(n)
		h, err := panel()
		if err != nil {
			return err
		}
		if got := strings.Count(h, `<p class="np-note">`+blurb+`</p>`); got != want {
			return fmt.Errorf("the blurb is shown %d times, want %d: %s", got, want, h)
		}
		return nil
	})
	sc.Step(`^the list shows both tracks$`, func() error {
		h, err := panel()
		if err != nil {
			return err
		}
		if !strings.Contains(h, "Song One") || !strings.Contains(h, "Song Two") {
			return fmt.Errorf("a track is missing: %s", h)
		}
		return nil
	})
	sc.Step(`^the list shows "([^"]*)" above "([^"]*)"$`, func(first, second string) error {
		h, err := panel()
		if err != nil {
			return err
		}
		a, b := strings.Index(h, first), strings.Index(h, second)
		if a < 0 || b < 0 || a > b {
			return fmt.Errorf("%q is not above %q: %s", first, second, h)
		}
		return nil
	})
	blurbOf := func() (string, error) {
		h, err := panel()
		if err != nil {
			return "", err
		}
		m := regexp.MustCompile(`<p class="np-note">([^<]*)</p>`).FindStringSubmatch(h)
		if m == nil {
			return "", fmt.Errorf("no blurb in %s", h)
		}
		return m[1], nil
	}
	sc.Step(`^the blurb ends on a whole word followed by an ellipsis$`, func() error {
		b, err := blurbOf()
		if err != nil {
			return err
		}
		body, ok := strings.CutSuffix(b, "…")
		if !ok {
			return fmt.Errorf("no ellipsis: %q", b)
		}
		f := strings.Fields(body)
		if !regexp.MustCompile(`^word\d\d$`).MatchString(f[len(f)-1]) {
			return fmt.Errorf("cut inside a word: %q", b)
		}
		return nil
	})
	sc.Step(`^the blurb is at most (\d+) characters$`, func(n string) error {
		b, err := blurbOf()
		if err != nil {
			return err
		}
		max, _ := strconv.Atoi(n)
		if c := len([]rune(b)); c > max {
			return fmt.Errorf("the blurb is %d characters", c)
		}
		return nil
	})
}

func (w *mapPolishWorld) addTrack(title, album, artist, blurb string) {
	w.plays = append(w.plays, map[string]any{"id": "t" + title, "track": title, "artist": artist, "album": album, "year": 1970,
		"started_at": "2026-11-12T12:00:00Z", "note": blurb})
}

// run feeds the page's own scripts through the harness. steps overrides the
// scenario's own steps when not nil.
func (w *mapPolishWorld) run(probes [][2]float64, steps []map[string]any) (map[string]any, error) {
	if steps == nil {
		steps = w.steps
	}
	if w.size == nil {
		w.size = map[string]int{"w": 375, "h": 380}
	}
	in := map[string]any{
		"data":   map[string]any{"legs": orEmpty(w.legs), "tracks": orEmpty(w.tracks), "checkpoints": orEmpty(w.cps), "sights": []any{}, "posts": []any{}},
		"size":   w.size,
		"steps":  steps,
		"probes": probes,
	}
	if len(w.plays) > 0 {
		in["tracks"] = map[string]any{"now": nil, "last": w.plays[0], "tracks": w.plays}
		in["data"].(map[string]any)["tracks"] = orEmpty(w.tracks)
	}
	// RIDE.tracks is the driven line; RIDE_TRACKS is the plays: the harness
	// reads them under different keys.
	return runNode(w.t, "static/js/map_harness.mjs", in)
}

func orEmpty(v []map[string]any) any {
	if v == nil {
		return []any{}
	}
	return v
}
