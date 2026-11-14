package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/driver_page_picks_the_next_day_after_the_night_stop.feature.
// They ride the two-day synthetic trip of the marks feature (Kingston,
// Napanee, Belleville on day 1; Trenton, Cobourg, Toronto on day 2) and read
// what /driver/hud.json answers for a fix near the end of day 1's line.
type dayPickWorld struct {
	t   *testing.T
	m   *marksWorld
	hud hudView
}

func initDayPickScenarios(t *testing.T, sc *godog.ScenarioContext, m *marksWorld) {
	d := &dayPickWorld{t: t, m: m}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*d = dayPickWorld{t: t, m: m}
		return ctx, nil
	})
	sc.Step(`^every stop of day 1 is arrived at$`, func() error { return d.settle(3, false) })
	sc.Step(`^the first two stops of day 1 are arrived at$`, func() error { return d.settle(2, false) })
	sc.Step(`^the first two stops of day 1 are arrived at and the last is skipped$`, func() error { return d.settle(2, true) })
	sc.Step(`^the Driver page is asked with a position a few miles back along day 1's line from its last stop$`, d.ask)
	sc.Step(`^the Driver page reads day (\d)$`, d.readsDay)
	sc.Step(`^the next stop is the first stop of day 2 and offers navigation$`, func() error { return d.nextIs("Trenton") })
	sc.Step(`^the next stop is the last stop of day 1 and offers navigation$`, func() error { return d.nextIs("Belleville") })
}

func (d *dayPickWorld) settle(arrived int, skipLast bool) error {
	rv, err := d.m.sim.app.loadRide()
	if err != nil {
		return err
	}
	cps := rv.Legs[0].Checkpoints
	for i, c := range cps {
		at := time.Date(2026, 11, 6, 9+i, 0, 0, 0, time.UTC)
		switch {
		case i < arrived:
			if err := d.m.sim.app.store.SetArrived(c.ID, at, nil); err != nil {
				return err
			}
		case skipLast && i == len(cps)-1:
			if err := d.m.sim.app.store.SetSkipped(c.ID, at, "not needed"); err != nil {
				return err
			}
		}
	}
	return nil
}

// ask puts the fix 85 percent of the way from Napanee to Belleville, about
// five kilometres short of the night stop and on day 1's own line.
func (d *dayPickWorld) ask() error {
	a, b := mhTowns["Napanee"], mhTowns["Belleville"]
	lat, lng := a[0]+0.85*(b[0]-a[0]), a[1]+0.85*(b[1]-a[1])
	target := fmt.Sprintf("/driver/hud.json?lat=%.5f&lng=%.5f", lat, lng)
	w := doAs(d.t, d.m.sim.app, d.m.sim.cookies["kris"], "GET", target, nil)
	if w.Code != http.StatusOK {
		return fmt.Errorf("hud.json answered %d: %s", w.Code, w.Body.String())
	}
	d.hud = hudView{}
	return json.Unmarshal(w.Body.Bytes(), &d.hud)
}

func (d *dayPickWorld) readsDay(n int) error {
	if d.hud.DayN != n {
		return fmt.Errorf("the page reads day %d, want day %d", d.hud.DayN, n)
	}
	return nil
}

func (d *dayPickWorld) nextIs(name string) error {
	if !d.hud.HasNext || d.hud.NextName != name {
		return fmt.Errorf("next stop is %q (has_next %v), want %q", d.hud.NextName, d.hud.HasNext, name)
	}
	if d.hud.Waze == "" || d.hud.Google == "" || d.hud.Apple == "" {
		return fmt.Errorf("no Navigate links for %s: %+v", name, d.hud)
	}
	return nil
}
