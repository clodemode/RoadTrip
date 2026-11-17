package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/map_on_a_phone.feature. The page's own map.js (and the
// shared map_common.js the Ride, leg and Plan maps build from) run under
// static/js/map_harness.mjs, a stand-in for Leaflet and the page: touches, a
// clock, buttons and a grouping of the pins that overlap. It is not a browser:
// the plugin's own clustering, the touch handling of a real phone, paint order
// and the CSS cascade are not modelled. What a stylesheet owes (a height, a
// 44 pixel target, a stacking order) is read from static/css/app.css as text.

type mapPhoneWorld struct {
	t      *testing.T
	touch  bool
	mode   string
	legs   []map[string]any
	cps    []map[string]any
	posts  []map[string]any
	plays  []map[string]any
	steps  []map[string]any
	vh     int
	out    map[string]any
	before map[string]any
}

func initMapPhoneScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &mapPhoneWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = mapPhoneWorld{t: t, mode: "ride"}
		return ctx, nil
	})
	add := func(m map[string]any) { w.steps = append(w.steps, m) }
	ride := func() {
		line := func(a ...[2]float64) [][]float64 {
			var o [][]float64
			for _, p := range a {
				o = append(o, []float64{p[1], p[0]})
			}
			return o
		}
		w.legs = []map[string]any{
			{"ord": 1, "slug": "d1", "name": "Day 1", "date": "2026-11-10", "color": "#0d6efd", "line": line(mpStart, mpDay1)},
			{"ord": 2, "slug": "d2", "name": "Day 2", "date": "2026-11-11", "color": "#dc2626", "line": line(mpDay1, mpMid, mpFinish)},
		}
		cp := func(ord int, name, mark, leg string, p [2]float64, isStart bool) map[string]any {
			return map[string]any{"ord": ord, "name": name, "mark": mark, "lat": p[0], "lng": p[1], "tz": "America/Chicago", "leg_slug": leg, "is_start": isStart}
		}
		w.cps = []map[string]any{cp(1, "Start Town", "d0", "d1", mpStart, true), cp(2, "Day One Town", "d1", "d1", mpDay1, false),
			cp(3, "Truck Stop", "3", "d2", [2]float64{mpDay1[0] + 0.0005, mpDay1[1]}, false), cp(4, "Finish Town", "d2", "d2", mpFinish, false)}
	}
	post := func(id int, lat, lng float64) {
		w.posts = append(w.posts, map[string]any{"id": id, "kind": "photo", "title": "", "when": "Tue", "lat": lat, "lng": lng,
			"thumb": "/media/t.jpg", "pin_source": "exif", "leg_slug": "d1"})
	}

	// --- one finger or two
	sc.Step(`^the Ride map is opened on a phone$`, func() error { ride(); w.touch = true; return nil })
	sc.Step(`^the Ride map is opened with a mouse$`, func() error { ride(); w.touch = false; return nil })
	sc.Step(`^one finger moves on the map$`, func() error { add(map[string]any{"do": "finger", "n": 1}); return nil })
	sc.Step(`^two fingers move on the map$`, func() error { add(map[string]any{"do": "finger", "n": 2}); return nil })
	sc.Step(`^the map is not set to pan under a single finger$`, func() error {
		o, err := w.run(nil)
		if err != nil {
			return err
		}
		if d, ok := opt(o, "dragging"); !ok || d != false {
			return fmt.Errorf("on a touch screen the map still pans under one finger: dragging is %v", d)
		}
		return nil
	})
	sc.Step(`^a hint says "([^"]*)"$`, func(text string) error {
		o, err := w.run(nil)
		if err != nil {
			return err
		}
		h, _ := o["hint"].(map[string]any)
		if h == nil {
			return fmt.Errorf("the map has no hint element")
		}
		if h["shown"] != true || h["text"] != text {
			return fmt.Errorf("the hint is %v, want %q shown", h, text)
		}
		if h["role"] != "status" {
			return fmt.Errorf("the hint is not a status message (role %v)", h["role"])
		}
		return nil
	})
	sc.Step(`^the hint is gone two seconds later$`, func() error {
		o, err := w.run([]map[string]any{{"do": "wait", "ms": 2000}})
		if err != nil {
			return err
		}
		if h, _ := o["hint"].(map[string]any); h == nil || h["shown"] != false {
			return fmt.Errorf("the hint is still shown after two seconds: %v", h)
		}
		return nil
	})
	sc.Step(`^moving and pinching with two fingers stay on$`, func() error {
		o, err := w.run(nil)
		if err != nil {
			return err
		}
		if d, ok := opt(o, "dragging"); !ok || d != false {
			return fmt.Errorf("one-finger dragging is on (%v), so two fingers are not the way to move the map", d)
		}
		if z, ok := opt(o, "touchZoom"); ok && (z == false || z == "center") {
			return fmt.Errorf("touchZoom is %v: two fingers cannot move and pinch", z)
		}
		return nil
	})
	sc.Step(`^no hint is shown$`, func() error {
		o, err := w.run(nil)
		if err != nil {
			return err
		}
		h, _ := o["hint"].(map[string]any)
		if h == nil {
			return fmt.Errorf("the map has no hint element, so two fingers were never told apart from one")
		}
		if h["shown"] == true {
			return fmt.Errorf("two fingers showed the hint")
		}
		return nil
	})
	sc.Step(`^dragging with the mouse is on$`, func() error {
		o, err := w.run(nil)
		if err != nil {
			return err
		}
		if d, ok := opt(o, "dragging"); ok && d == false {
			return fmt.Errorf("a mouse cannot drag the map")
		}
		return nil
	})
	sc.Step(`^the mouse wheel does not zoom the map$`, func() error {
		o, err := w.run(nil)
		if err != nil {
			return err
		}
		if v, _ := opt(o, "scrollWheelZoom"); v != false {
			return fmt.Errorf("scrollWheelZoom is %v", v)
		}
		return nil
	})

	// --- gathering
	sc.Step(`^the Ride map is opened with (\d+) photos and a music note within 200 metres of one stop$`, func(n string) error {
		ride()
		w.touch = true
		c, _ := strconv.Atoi(n)
		for k := 0; k < c; k++ {
			post(100+k, mpDay1[0]+0.0004*float64(k), mpDay1[1]+0.0004)
		}
		w.plays = []map[string]any{{"id": "p1", "track": "Song", "artist": "A", "album": "Al", "started_at": "2026-11-12T12:00:00Z", "lat": mpDay1[0] + 0.0012, "lng": mpDay1[1] + 0.0006}}
		return nil
	})
	sc.Step(`^the (\d+) photos and the music note gather into one count of (\d+)$`, func(_, n string) error {
		want, _ := strconv.Atoi(n)
		o, err := w.run(nil)
		if err != nil {
			return err
		}
		g, _ := o["gathered"].([]any)
		if len(g) != 1 || int(g[0].(map[string]any)["count"].(float64)) != want {
			return fmt.Errorf("the pins are %d group(s) %v, want one count of %d", len(g), g, want)
		}
		return nil
	})
	sc.Step(`^the count is a round badge with the number (\d+)$`, func(n string) error {
		o, err := w.run(nil)
		if err != nil {
			return err
		}
		g, _ := o["gathered"].([]any)
		if len(g) == 0 || !strings.Contains(g[0].(map[string]any)["badge"].(string), ">"+n+"<") {
			return fmt.Errorf("the count does not show %s: %v", n, g)
		}
		css := appCSS(w.t)
		if !regexp.MustCompile(`\.rt-gather[^{]*\{[^}]*border-radius:\s*50%`).MatchString(css) {
			return fmt.Errorf("no .rt-gather rule makes the count round")
		}
		return nil
	})
	sc.Step(`^the stop's pins and the day pin are never gathered$`, func() error {
		o, err := w.run(nil)
		if err != nil {
			return err
		}
		stops := 0
		for _, m := range o["markers"].([]any) {
			mm := m.(map[string]any)
			cls := fmt.Sprint(mm["cls"])
			if strings.Contains(cls, "cp-pin") || strings.Contains(cls, "day-pin") {
				stops++
				if mm["clustered"] == true {
					return fmt.Errorf("a stop pin (%s) is inside a gathering group", cls)
				}
			}
			if (strings.Contains(cls, "post-pin") || strings.Contains(cls, "np-pin")) && mm["clustered"] != true {
				return fmt.Errorf("a photo or note (%s) is drawn outside the gathering group", cls)
			}
		}
		if stops < 2 {
			return fmt.Errorf("only %d stop pin(s) were drawn: the check cannot fire", stops)
		}
		gp, _ := o["gatherOpts"].([]any)
		if len(gp) == 0 {
			return fmt.Errorf("no gathering group was made")
		}
		for _, g := range gp {
			gm := g.(map[string]any)
			if r, _ := gm["maxClusterRadius"].(float64); r < 34 {
				return fmt.Errorf("pins gather only within %v px, less than a 34 px photo pin: two touching pins would not gather", gm["maxClusterRadius"])
			}
			if gm["zoomToBoundsOnClick"] == false || gm["spiderfyOnMaxZoom"] == false {
				return fmt.Errorf("a tap on a count does not open it: %v", gm)
			}
		}
		return nil
	})
	sc.Step(`^the visitor taps the count$`, func() error { add(map[string]any{"do": "tapCluster"}); return nil })
	sc.Step(`^the pins spread into more than one group$`, func() error {
		o, err := w.run(nil)
		if err != nil {
			return err
		}
		if g, _ := o["gathered"].([]any); len(g) < 2 {
			return fmt.Errorf("after the tap the pins are still %d group(s) at zoom %v", len(g), (o["view"].(map[string]any))["zoom"])
		}
		return nil
	})

	// --- size and full screen
	sc.Step(`^a phone (\d+) pixels wide and (\d+) tall$`, func(_, h string) error { w.vh, _ = strconv.Atoi(h); return nil })
	sc.Step(`^the map is taller than (\d+) pixels$`, func(n string) error {
		min, _ := strconv.Atoi(n)
		h, err := cssHeight(appCSS(w.t), w.vh)
		if err != nil {
			return err
		}
		if h <= float64(min) {
			return fmt.Errorf("the map is %.0f px tall on a phone %d px tall, want more than %d", h, w.vh, min)
		}
		return nil
	})
	sc.Step(`^Full screen is a class on the map with the window fixed and no dialog$`, func() error {
		css := appCSS(w.t)
		rule := ruleOf(css, `#map.rt-map-full`)
		if rule == "" {
			return fmt.Errorf("no #map.rt-map-full rule")
		}
		if !strings.Contains(rule, "position: fixed") && !strings.Contains(rule, "position:fixed") {
			return fmt.Errorf("full screen is not fixed to the window: %s", rule)
		}
		if !regexp.MustCompile(`inset:\s*0|top:\s*0[^}]*bottom:\s*0`).MatchString(rule) {
			return fmt.Errorf("full screen does not reach every edge of the window: %s", rule)
		}
		if m := regexp.MustCompile(`z-index:\s*(\d+)`).FindStringSubmatch(rule); m == nil {
			return fmt.Errorf("full screen has no stacking order: %s", rule)
		}
		for _, f := range []string{"static/js/map_common.js", "static/js/map.js", "static/js/plan.js"} {
			b, err := staticFS.ReadFile(f)
			if err != nil {
				return fmt.Errorf("%s: %v (the maps have no shared helper yet)", f, err)
			}
			if regexp.MustCompile(`(?i)requestFullscreen|webkitRequestFullscreen|showModal|<dialog`).Match(b) {
				return fmt.Errorf("%s asks the browser for a full screen or a dialog", f)
			}
		}
		return nil
	})
	sc.Step(`^the visitor presses Full screen$`, func() error { add(map[string]any{"do": "control", "name": "full"}); return nil })
	sc.Step(`^the visitor presses Done$`, func() error { add(map[string]any{"do": "control", "name": "done"}); return nil })
	sc.Step(`^the visitor presses the Escape key$`, func() error { add(map[string]any{"do": "key", "key": "Escape"}); return nil })
	sc.Step(`^the map fills the window in place$`, func() error {
		o, err := w.run(nil)
		if err != nil {
			return err
		}
		f, _ := o["full"].(map[string]any)
		if f == nil || f["onMap"] != true {
			return fmt.Errorf("the map did not take the full screen class: %v", f)
		}
		if f["onBody"] != true {
			return fmt.Errorf("the page behind the full screen map can still scroll")
		}
		base, err := w.run(nil, "control")
		if err != nil {
			return err
		}
		if o["invalidated"].(float64) <= base["invalidated"].(float64) {
			return fmt.Errorf("the map was not told its box changed (invalidateSize)")
		}
		return nil
	})
	sc.Step(`^a Done button is shown$`, func() error { return w.done(false) })
	sc.Step(`^a Done button is not shown$`, func() error { return w.done(true) })
	sc.Step(`^the map is back in the page$`, func() error {
		o, err := w.run(nil)
		if err != nil {
			return err
		}
		f, _ := o["full"].(map[string]any)
		if f == nil || f["onMap"] != false || f["onBody"] != false {
			return fmt.Errorf("the map is still full screen: %v", f)
		}
		return nil
	})

	// --- controls
	sc.Step(`^the zoom buttons and the layers button are at least (\d+) by (\d+) pixels$`, func(a, b string) error {
		css := appCSS(w.t)
		for _, sel := range []string{`.leaflet-bar a`, `.leaflet-control-layers-toggle`} {
			if err := bigEnough(css, sel, 44); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the Fit and Full screen and Done buttons are at least (\d+) by (\d+) pixels$`, func(a, b string) error {
		return bigEnough(appCSS(w.t), `.rt-map-btn`, 44)
	})
	sc.Step(`^the Plan map takes the same controls as the Ride map$`, func() error {
		plan, err := staticFS.ReadFile("static/js/plan.js")
		if err != nil {
			return err
		}
		if regexp.MustCompile(`\bL\.map\(`).Match(plan) {
			return fmt.Errorf("plan.js builds its own L.map: the options and buttons must come from the one shared helper")
		}
		if !strings.Contains(string(plan), "RTMap.") {
			return fmt.Errorf("plan.js does not use the shared helper (RTMap)")
		}
		for _, tpl := range []string{"templates/plan.html", "templates/index.html", "templates/leg.html"} {
			b, err := templateFS.ReadFile(tpl)
			if err != nil {
				return err
			}
			src := string(b)
			c, p := strings.Index(src, "/static/js/map_common.js"), strings.Index(src, "/static/js/map.js")
			if p < 0 {
				p = strings.Index(src, "/static/js/plan.js")
			}
			if c < 0 || p < 0 || c > p {
				return fmt.Errorf("%s does not load map_common.js before its map script", tpl)
			}
		}
		return nil
	})
	sc.Step(`^full screen sits above the Listen Inn card$`, func() error {
		css := appCSS(w.t)
		z := func(rule string) int {
			m := regexp.MustCompile(`z-index:\s*(\d+)`).FindStringSubmatch(rule)
			if m == nil {
				return -1
			}
			n, _ := strconv.Atoi(m[1])
			return n
		}
		dock, full := z(ruleOf(css, `.listen-dock`)), z(ruleOf(css, `#map.rt-map-full`))
		if dock < 0 || full <= dock {
			return fmt.Errorf("full screen (z-index %d) does not sit above the Listen Inn card (z-index %d)", full, dock)
		}
		return nil
	})

	// --- fit
	sc.Step(`^the Ride map offers a Fit trip button$`, func() error { return w.fitButton("Fit trip") })
	sc.Step(`^the visitor moves the map and presses Fit trip$`, func() error {
		add(map[string]any{"do": "pan", "dx": 480, "dy": 300})
		return nil
	})
	sc.Step(`^the view is the one the page opened with$`, func() error {
		moved, err := w.run(nil, "control")
		if err != nil {
			return err
		}
		if fmt.Sprint(moved["view"]) == fmt.Sprint(moved["opening"]) {
			return fmt.Errorf("the drag did not move the view: the check cannot fire")
		}
		// Fit trip is pressed now.
		o, err := w.run([]map[string]any{{"do": "control", "name": "fit"}})
		if err != nil {
			return err
		}
		if fmt.Sprint(o["view"]) != fmt.Sprint(o["opening"]) {
			return fmt.Errorf("Fit trip left the view at %v, the page opened at %v", o["view"], o["opening"])
		}
		return nil
	})

	// --- the leg
	sc.Step(`^the leg map of Winchester to Nashville with a post pinned far from the road$`, func() error {
		w.mode, w.touch = "leg", true
		road := [][]float64{{-78.16, 39.19}, {-80.4, 37.1}, {-82.3, 36.6}, {-83.92, 35.96}, {-86.74, 36.23}}
		w.legs = []map[string]any{{"ord": 2, "slug": "d2-i81-three_winchester_nashville", "name": "Winchester VA to Nashville", "date": "2026-11-12", "color": "#dc2626", "line": road}}
		w.cps = []map[string]any{
			{"ord": 5, "name": "Knoxville TN", "mark": "5", "lat": 35.96, "lng": -83.92, "tz": "America/New_York", "leg_slug": "d2-i81-three_winchester_nashville"},
			{"ord": 6, "name": "Nashville", "mark": "d2", "lat": 36.23, "lng": -86.74, "tz": "America/Chicago", "leg_slug": "d2-i81-three_winchester_nashville"},
		}
		post(61, 36.58, -82.28)
		post(62, 38.64, -78.59)
		post(70, 44.0, -60.0) // pinned off the road, far to the north-east
		return nil
	})
	sc.Step(`^the leg's line and its two checkpoints are inside the opening view$`, func() error {
		probes := [][2]float64{{39.19, -78.16}, {36.23, -86.74}, {35.96, -83.92}}
		o, err := w.run(nil, "probes", probes)
		if err != nil {
			return err
		}
		for k, c := range o["contains"].([]any) {
			if c != true {
				return fmt.Errorf("point %v of the leg is outside the opening view (zoom %v)", probes[k], o["zoom"])
			}
		}
		return nil
	})
	sc.Step(`^the leg map offers a Fit leg button$`, func() error { return w.fitButton("Fit leg") })
	sc.Step(`^the stray post does not widen the opening view$`, func() error {
		with, err := w.run(nil)
		if err != nil {
			return err
		}
		keep := w.posts
		w.posts = nil
		for _, p := range keep {
			if p["id"] != 70 {
				w.posts = append(w.posts, p)
			}
		}
		without, err := w.run(nil)
		w.posts = keep
		if err != nil {
			return err
		}
		if fmt.Sprint(with["opening"]) != fmt.Sprint(without["opening"]) {
			return fmt.Errorf("a post pinned far from the road moved the leg's opening view from %v to %v", without["opening"], with["opening"])
		}
		return nil
	})
}

func opt(o map[string]any, k string) (any, bool) {
	m, _ := o["mapOpts"].(map[string]any)
	v, ok := m[k]
	return v, ok
}

func (w *mapPhoneWorld) fitButton(label string) error {
	o, err := w.run(nil)
	if err != nil {
		return err
	}
	for _, b := range o["buttons"].([]any) {
		bm := b.(map[string]any)
		if bm["name"] == "fit" {
			if bm["label"] != label {
				return fmt.Errorf("the fit button says %q, want %q", bm["label"], label)
			}
			return nil
		}
	}
	return fmt.Errorf("the map has no Fit button (buttons: %v)", o["buttons"])
}

func (w *mapPhoneWorld) done(hidden bool) error {
	o, err := w.run(nil)
	if err != nil {
		return err
	}
	for _, b := range o["buttons"].([]any) {
		bm := b.(map[string]any)
		if bm["name"] == "done" {
			if bm["hidden"] != hidden {
				return fmt.Errorf("the Done button hidden=%v, want %v", bm["hidden"], hidden)
			}
			return nil
		}
	}
	return fmt.Errorf("the map has no Done button (buttons: %v)", o["buttons"])
}

// run feeds the page's scripts through the harness. extra steps follow the
// scenario's own. An extra of "control" drops the scenario's control and key
// steps (the page as it was before the visitor pressed anything); "probes"
// takes the points to test as the next argument.
func (w *mapPhoneWorld) run(extra []map[string]any, mods ...any) (map[string]any, error) {
	steps := []map[string]any{}
	var probes [][2]float64
	for k := 0; k < len(mods); k++ {
		if mods[k] == "probes" {
			probes = mods[k+1].([][2]float64)
			k++
		}
	}
	dropControls := len(mods) > 0 && mods[0] == "control"
	for _, s := range w.steps {
		if dropControls && (s["do"] == "control" || s["do"] == "key") {
			continue
		}
		steps = append(steps, s)
	}
	steps = append(steps, extra...)
	in := map[string]any{
		"data":   map[string]any{"legs": orEmpty(w.legs), "tracks": []any{}, "checkpoints": orEmpty(w.cps), "sights": []any{}, "posts": orEmpty(w.posts)},
		"size":   map[string]int{"w": 375, "h": 500},
		"steps":  steps,
		"probes": probes,
		"touch":  w.touch,
		"mode":   w.mode,
	}
	if len(w.plays) > 0 {
		in["tracks"] = map[string]any{"now": nil, "last": w.plays[0], "tracks": w.plays}
	}
	return runNode(w.t, "static/js/map_harness.mjs", in)
}

// --- the stylesheet, read as text

func appCSS(t *testing.T) string {
	t.Helper()
	b, err := staticFS.ReadFile("static/css/app.css")
	if err != nil || len(b) == 0 {
		t.Fatalf("app.css: %v", err)
	}
	return regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(b), "")
}

// ruleOf is the body of the first rule whose selector list names sel exactly
// as one of its selectors; empty when there is none.
func ruleOf(css, sel string) string {
	for _, m := range regexp.MustCompile(`([^{}]+)\{([^}]*)\}`).FindAllStringSubmatch(css, -1) {
		for _, s := range strings.Split(m[1], ",") {
			if strings.TrimSpace(s) == sel {
				return m[2]
			}
		}
	}
	return ""
}

// bigEnough: some rule that names sel (under #map, so it beats Leaflet's own
// selectors) declares both a width and a height of at least min pixels.
func bigEnough(css, sel string, min int) error {
	var wd, ht int
	for _, m := range regexp.MustCompile(`([^{}]+)\{([^}]*)\}`).FindAllStringSubmatch(css, -1) {
		for _, s := range strings.Split(m[1], ",") {
			s = strings.TrimSpace(s)
			if !strings.HasSuffix(s, sel) || !strings.Contains(s, "#map") {
				continue
			}
			for _, d := range regexp.MustCompile(`(?:^|[;\s])(min-width|width|min-height|height)\s*:\s*(\d+)px`).FindAllStringSubmatch(m[2], -1) {
				n, _ := strconv.Atoi(d[2])
				if strings.HasSuffix(d[1], "width") && n > wd {
					wd = n
				}
				if strings.HasSuffix(d[1], "height") && n > ht {
					ht = n
				}
			}
		}
	}
	if wd < min || ht < min {
		return fmt.Errorf("%s under #map is %dx%d px, want at least %dx%d", sel, wd, ht, min, min)
	}
	return nil
}

// cssHeight is the map's height at the base (phone) width, for a window vh
// tall: a px figure, or min(Nvh, Mpx).
func cssHeight(css string, vh int) (float64, error) {
	r := ruleOf(css, `#map`)
	m := regexp.MustCompile(`height:\s*([^;]+);`).FindStringSubmatch(r)
	if m == nil {
		return 0, fmt.Errorf("#map has no height")
	}
	v := strings.TrimSpace(m[1])
	if p := regexp.MustCompile(`^min\(\s*(\d+)vh\s*,\s*(\d+)px\s*\)$`).FindStringSubmatch(v); p != nil {
		a, _ := strconv.Atoi(p[1])
		b, _ := strconv.Atoi(p[2])
		h := float64(vh) * float64(a) / 100
		if h > float64(b) {
			h = float64(b)
		}
		return h, nil
	}
	if p := regexp.MustCompile(`^(\d+)px$`).FindStringSubmatch(v); p != nil {
		n, _ := strconv.Atoi(p[1])
		return float64(n), nil
	}
	return 0, fmt.Errorf("#map height %q is not px or min(vh, px)", v)
}
