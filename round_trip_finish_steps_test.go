package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/round_trip_finish_is_the_next_stop.feature, on the
// postWorld and the A..D trip of start_checkin_steps_test.go, with D moved
// onto A and the route bent back home east of the outbound line.
type roundTripWorld struct {
	p   *postWorld
	hud hudView
}

func initRoundTripScenarios(sc *godog.ScenarioContext, p *postWorld) {
	w := &roundTripWorld{p: p}
	sc.Step(`^the active trip is a round trip: A, B and C in order, then the finish D back at A$`, w.build)
	sc.Step(`^the round trip's B and C are arrived at$`, func() error { return w.arrive(1, 2) })
	sc.Step(`^the round trip's B is arrived at$`, func() error { return w.arrive(1) })
	sc.Step(`^the driver screen is asked with no position$`, w.ask)
	sc.Step(`^the next stop on the driver screen is ([ABCD]), with Waze, Google and Apple links$`, w.nextIs)
}

func (w *roundTripWorld) build() error {
	s := &scWorld{p: w.p}
	if err := s.line(); err != nil {
		return err
	}
	// Out A, B, C up the line; home by a road 25 km east of it; the finish on A.
	line := [][]float64{{stopsLng, stopLat(0)}, {stopsLng, stopLat(1)}, {stopsLng, stopLat(2)},
		{stopsLng + 0.3, stopLat(1)}, {stopsLng, stopLat(0)}}
	raw, _ := json.Marshal(map[string]any{"type": "LineString", "coordinates": line})
	if _, err := w.p.sim.app.store.db.Exec(`UPDATE legs SET line_geojson=? WHERE id=?`, string(raw), w.p.cps[0].LegID); err != nil {
		return err
	}
	return s.moveStop(3, stopLat(0), stopsLng)
}

func (w *roundTripWorld) arrive(idx ...int) error {
	for _, i := range idx {
		if err := w.p.sim.app.store.SetArrived(w.p.cps[i].ID, time.Date(2030, 5, 1, 10+i, 0, 0, 0, time.UTC), nil); err != nil {
			return err
		}
	}
	return nil
}

func (w *roundTripWorld) ask() error {
	rec := doAs(w.p.t, w.p.sim.app, w.p.sim.cookies["kris"], "GET", "/driver/hud.json", nil)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("hud.json answered %d: %s", rec.Code, rec.Body.String())
	}
	w.hud = hudView{}
	return json.Unmarshal(rec.Body.Bytes(), &w.hud)
}

func (w *roundTripWorld) nextIs(which string) error {
	c := w.p.cps[int(which[0]-'A')]
	if !w.hud.HasNext || w.hud.NextName != c.Name {
		return fmt.Errorf("the next stop is %q (has_next %v), want %s %q", w.hud.NextName, w.hud.HasNext, which, c.Name)
	}
	if w.hud.Waze == "" || w.hud.Google == "" || w.hud.Apple == "" {
		return fmt.Errorf("no Navigate links for %s: waze %q google %q apple %q", which, w.hud.Waze, w.hud.Google, w.hud.Apple)
	}
	return nil
}
