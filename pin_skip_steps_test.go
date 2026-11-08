package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/photo_pin_and_skip_reason.feature. They ride the start-
// and-check-in world and add the Kingston to Toronto trip: seven stops at
// their public town-centre coordinates along the sim corridor's own polyline.
// The trip is built here, in memory, and is synthetic: nothing of it is a
// file in the repo.
type pinWorld struct {
	p *postWorld
	s *scWorld
}

type kgnTown struct {
	name     string
	lat, lng float64
}

var kgnTowns = []kgnTown{
	{"Kingston", 44.2312, -76.4860}, {"Napanee", 44.2487, -76.9500}, {"Belleville", 44.1628, -77.3832},
	{"Trenton", 44.1000, -77.5780}, {"Cobourg", 43.9593, -78.1677}, {"Oshawa", 43.8971, -78.8658},
	{"Toronto", 43.6532, -79.3832},
}

func initPinSkipScenarios(sc *godog.ScenarioContext, p *postWorld, s *scWorld) {
	w := &pinWorld{p: p, s: s}
	sc.Step(`^the drive has started on the Kingston to Toronto trip$`, w.kingstonStarted)
	sc.Step(`^kris's simulated car is near Belleville$`, w.carNearBelleville)
	sc.Step(`^a photo taken with its GPS 1500 km from every stop$`, func() error {
		// Hudson Bay: more than 1500 km from every stop on the corridor.
		p.photo = jpegGPSAt("2026:11:07 11:30:00", 58.0, -78.0)
		return nil
	})
	sc.Step(`^the phone reports a position near (Belleville|Toronto)$`, func(name string) error {
		t := w.town(name)
		s.phone, s.phoneAt = true, &[2]float64{t.lat, t.lng}
		return nil
	})
	sc.Step(`^kris posts a photo$`, func() error {
		p.photo = jpegTaken("")
		return s.send(-1, true, s.phone)
	})
	sc.Step(`^the post is pinned near (Belleville)$`, w.pinnedNear)
	sc.Step(`^the post's pin source is "([a-z]+)"$`, w.pinSource)
	sc.Step(`^the post is not pinned at the photo's own GPS$`, w.notAtPhotoGPS)
	sc.Step(`^(Belleville|Toronto) is arrived at$`, func(name string) error { return s.arrived(w.index(name)) })
	sc.Step(`^([A-Z][a-z]+(?:, [A-Z][a-z]+)*) and ([A-Z][a-z]+) are skipped as "([a-z]+)"$`, w.skippedAll)
	sc.Step(`^the form requests the phone's location as soon as it opens$`, w.formAsksOnOpen)
	sc.Step(`^the form sends the phone's position with the post when it has one$`, w.formSends)
	sc.Step(`^the notice says (Belleville) was reached by the simulated car$`, func(name string) error {
		return s.noticeHas("Reached "+name, "by the simulated car")
	})
}

func (w *pinWorld) town(name string) kgnTown {
	for _, t := range kgnTowns {
		if t.name == name {
			return t
		}
	}
	panic("no town " + name)
}

func (w *pinWorld) index(name string) int {
	for i, c := range w.p.cps {
		if c.Name == name {
			return i
		}
	}
	panic("no stop " + name)
}

// kingstonStarted pushes the Kingston to Toronto trip as the active one and
// departs Kingston.
func (w *pinWorld) kingstonStarted() error {
	p := w.p
	var line []any
	for _, c := range simCorridors[simCorKGN] {
		line = append(line, []any{c[1], c[0]})
	}
	feats := []any{map[string]any{
		"type":       "Feature",
		"properties": map[string]any{"leg": 1, "day": 1, "slug": "kingston-toronto", "name": "Kingston to Toronto", "est_miles": 160, "route_hint": "the 401"},
		"geometry":   map[string]any{"type": "LineString", "coordinates": line},
	}}
	for i, t := range kgnTowns {
		props := map[string]any{"kind": "checkpoint", "order": i + 1, "leg": 1, "name": t.name, "label": "Stop",
			"tz": "America/Toronto", "precision": "approx"}
		if i == 0 || i == len(kgnTowns)-1 {
			props["is_terminal"] = true
		}
		feats = append(feats, map[string]any{"type": "Feature", "properties": props,
			"geometry": map[string]any{"type": "Point", "coordinates": []any{t.lng, t.lat}}})
	}
	doc := map[string]any{"type": "FeatureCollection", "synthetic": true, "name": "Kingston to Toronto, synthetic",
		"ride":    map[string]any{"slug": "post-photo-trip", "name": "Kingston to Toronto", "start_date": "2030-05-01", "mode": "CAR"},
		"avg_mph": 60, "long_day_hours": 9.5, "features": feats}
	raw, _ := json.Marshal(doc)
	if r := pushTrip(p.t, p.sim.app, raw, "?activate=1"); r.Code != http.StatusOK {
		return fmt.Errorf("push: %d %s", r.Code, r.Body.String())
	}
	if err := w.s.reload(len(kgnTowns)); err != nil {
		return err
	}
	return p.sim.app.store.SetArrived(p.cps[0].ID, time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC), nil)
}

// carNearBelleville runs kris's simulated car and parks it at Belleville: the
// rung is the distance along the corridor from where the drive begins.
func (w *pinWorld) carNearBelleville() error {
	sim := w.p.sim
	if err := sim.started("kris", simCorKGN, 7, 60); err != nil {
		return err
	}
	st, ok := sim.app.sim.State("kris")
	if !ok {
		return fmt.Errorf("the simulated car is not running")
	}
	b := w.town("Belleville")
	along, _ := projectOnLine(simCorridors[simCorKGN], b.lat, b.lng)
	st.RungMi = along - simStartOffset(st.Seed)
	st.T0 = sim.now.Add(-24 * time.Hour)
	sim.app.sim.Start("kris", st)
	la, ln, _, _, _, _ := sim.app.sim.PositionAt("kris", sim.now)
	if km := haversineKm(la, ln, b.lat, b.lng); km > 5 {
		return fmt.Errorf("the car is %.1f km from Belleville", km)
	}
	return nil
}

func (w *pinWorld) post() (*Post, error) {
	posts, err := w.p.sim.app.store.Posts("post-photo-trip", 0)
	if err != nil {
		return nil, err
	}
	if len(posts) != 1 {
		return nil, fmt.Errorf("want one post, have %d (answer %d %s)", len(posts), w.p.last.Code, w.p.last.Body.String())
	}
	return &posts[0], nil
}

func (w *pinWorld) pinnedNear(name string) error {
	post, err := w.post()
	if err != nil {
		return err
	}
	if post.Lat == nil || post.Lng == nil {
		return fmt.Errorf("the post has no pin (source %q)", post.PinSource)
	}
	t := w.town(name)
	if km := haversineKm(*post.Lat, *post.Lng, t.lat, t.lng); km > 10 {
		return fmt.Errorf("the post is pinned %.0f km from %s", km, name)
	}
	return nil
}

func (w *pinWorld) pinSource(want string) error {
	post, err := w.post()
	if err != nil {
		return err
	}
	if post.PinSource != want {
		return fmt.Errorf("the post's pin source is %q, want %q", post.PinSource, want)
	}
	return nil
}

func (w *pinWorld) notAtPhotoGPS() error {
	post, err := w.post()
	if err != nil {
		return err
	}
	if post.Lat != nil && post.Lng != nil && haversineKm(*post.Lat, *post.Lng, 58.0, -78.0) < 100 {
		return fmt.Errorf("the post is pinned at the photo's own GPS")
	}
	return nil
}

func (w *pinWorld) skippedAll(list, last, why string) error {
	for _, n := range append(strings.Split(list, ", "), last) {
		if err := w.s.skipped(w.index(n), why); err != nil {
			return err
		}
	}
	return nil
}

func (w *pinWorld) script() (string, error) {
	r := doAs(w.p.t, w.p.sim.app, w.p.sim.cookies["kris"], "GET", "/static/js/post.js", nil)
	if r.Code != http.StatusOK {
		return "", fmt.Errorf("post.js answered %d", r.Code)
	}
	return r.Body.String(), nil
}

// formAsksOnOpen: the page carries the script, and the script asks the phone
// at load, from its top level, not from a radio's change handler.
// askAtLoad matches the call at the script's own level (two-space indent),
// not one nested inside a handler.
var askAtLoad = regexp.MustCompile(`(?m)^  askLocation\(navigator\.geolocation, `)

func (w *pinWorld) formAsksOnOpen() error {
	if !strings.Contains(w.p.body, `/static/js/post.js`) {
		return fmt.Errorf("the form does not load post.js")
	}
	js, err := w.script()
	if err != nil {
		return err
	}
	if !askAtLoad.MatchString(js) {
		return fmt.Errorf("post.js does not call askLocation(navigator.geolocation, ...) at load")
	}
	return nil
}

func (w *pinWorld) formSends() error {
	for _, f := range []string{"lat", "lng", "accuracy"} {
		if !strings.Contains(w.p.body, `type="hidden" name="`+f+`"`) {
			return fmt.Errorf("the form has no hidden %s field", f)
		}
	}
	js, err := w.script()
	if err != nil {
		return err
	}
	if !strings.Contains(js, "la.value = fix ? fix.lat") {
		return fmt.Errorf("post.js does not put the fix in the lat field")
	}
	return nil
}
