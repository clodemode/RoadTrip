package main

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/hours_name_milestones.feature. They ride the marks world:
// the same invented one-day Kingston to Toronto trip, re-pushed with a stop or
// a routing point laid a stated number of miles along its planned line. A
// private place is the existing "Home Base" fuzzed stop.
type hoursMsWorld struct {
	t      *testing.T
	m      *marksWorld
	stops  []float64 // extra stops, miles along the line
	stopNm []string
	rps    []float64 // routing points, miles along the line
	rpNm   []string
}

func initHoursMilestoneScenarios(t *testing.T, sc *godog.ScenarioContext, m *marksWorld) {
	w := &hoursMsWorld{t: t, m: m}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = hoursMsWorld{t: t, m: m}
		return ctx, nil
	})
	sc.Step(`^a routing point "([^"]*)" lies (\d+) miles along the plan$`, func(name string, mi int) error {
		w.rps, w.rpNm = append(w.rps, float64(mi)), append(w.rpNm, name)
		return w.push()
	})
	sc.Step(`^a stop "([^"]*)" lies (\d+) miles along the plan$`, func(name string, mi int) error {
		w.stops, w.stopNm = append(w.stops, float64(mi)), append(w.stopNm, name)
		return w.push()
	})
	sc.Step(`^the hours bar marks "(h\d)" as "([^"]*)"$`, func(h, name string) error {
		e, err := m.entry(h)
		if err != nil {
			return err
		}
		if e != h+" "+name {
			return fmt.Errorf("the hours bar marks %q, want %q", e, h+" "+name)
		}
		return nil
	})
	sc.Step(`^a routing point "([^"]*)" is stored beside that private stop$`, w.privateRouting)
	sc.Step(`^a private place is never named$`, func() error {
		e, err := m.entry("h1")
		if err != nil {
			return err
		}
		if e != "h1" {
			return fmt.Errorf("the hours bar marks %q, want the hour alone", e)
		}
		return nil
	})
}

// push lays the extra stops and routing points on the trip: Kingston, the extra
// stops in order, Toronto; each routing point follows the stops lying before it.
func (w *hoursMsWorld) push() error {
	legs := []mhStop{town("Kingston", false)}
	order := []int{}
	for i := range w.stops {
		order = append(order, i)
	}
	for a := 0; a < len(order); a++ {
		for b := a + 1; b < len(order); b++ {
			if w.stops[order[b]] < w.stops[order[a]] {
				order[a], order[b] = order[b], order[a]
			}
		}
	}
	for _, i := range order {
		p, _ := simWalkPath(mhLine, w.stops[i])
		legs = append(legs, mhStop{name: w.stopNm[i], lat: p[0], lng: p[1]})
	}
	legs = append(legs, town("Toronto", true))
	var feats []map[string]any
	for i, mi := range w.rps {
		after := 1
		for _, s := range w.stops {
			if s <= mi {
				after++
			}
		}
		p, _ := simWalkPath(mhLine, mi)
		feats = append(feats, map[string]any{"type": "Feature",
			"properties": map[string]any{"kind": "routing", "leg": 1, "after": after, "name": w.rpNm[i]},
			"geometry":   map[string]any{"type": "Point", "coordinates": []float64{p[1], p[0]}}})
	}
	return w.m.push([][]mhStop{legs}, 60, feats, mhLine)
}

// privateRouting writes a routing point straight into the table at the private
// stop's place, the way no document or form could (both refuse it): the hours
// bar must still not name it.
func (w *hoursMsWorld) privateRouting(name string) error {
	rv, err := w.m.sim.app.loadRide()
	if err != nil {
		return err
	}
	p := sixty(mhLine)
	_, err = w.m.sim.app.store.db.Exec(`INSERT INTO routing_points (leg_id,after_ord,name,lat,lng,source) VALUES (?,?,?,?,?,?)`,
		rv.Legs[0].ID, 1, name, p[0], p[1], routingFromRoad)
	if err != nil {
		return fmt.Errorf("storing the routing point %s: %w", strconv.Quote(name), err)
	}
	return nil
}
