package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// routingWorld drives the real mux with the push token, the way the trip-keeper's
// carriage does. Every place in it is invented (testdata/routing-trip-fixture.geojson).
type routingWorld struct {
	t    *testing.T
	app  *App
	slug string
	last *httptest.ResponseRecorder
	fix  [2]float64 // the car's position: lat, lng
	have bool
}

// Positions on the invented ridge road and on day 2, by the words the feature uses.
var routingFixes = map[string][2]float64{
	"just after Alder on the ridge road": {40.13, -100.44},
	"past Plum Bridge":                   {40.6, -100.68},
	"past Birch Gate":                    {41.2, -101.2},
	"past Quince Turn":                   {41.8, -101.8},
	"on day 2 before Pine Fork":          {42.25, -102.5},
	"on day 2 past Pine Fork":            {42.75, -103.5},
}

func initRoutingPointsScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &routingWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = routingWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^a trip whose document carries routing points$`, func() error {
		w.app = tokenApp(w.t, testToken)
		w.slug = "routing-fixture"
		raw, err := os.ReadFile(filepath.Join("testdata", "routing-trip-fixture.geojson"))
		if err != nil {
			return err
		}
		w.last = pushTrip(w.t, w.app, raw, "?activate=1")
		if w.last.Code != http.StatusOK {
			return fmt.Errorf("the document was not taken: %d %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^a trip whose document carries no routing points$`, func() {
		w.app = activeTripApp(w.t)
		w.slug = "fixture-trip"
	})
	sc.Step(`^the trip-keeper is signed in with the plan saved$`, func() {})
	sc.Step(`^the trip has (\d+) stops$`, func(n int) error {
		if got := rideCensus(w.t, w.app, w.slug).Checkpoints; got != n {
			return fmt.Errorf("the trip has %d stops, want %d", got, n)
		}
		return nil
	})
	sc.Step(`^a routing point is not a stop to check in at$`, func() error {
		// No stop in the ride carries a routing point's name.
		for _, l := range mustLegs(w.t, w.app, w.slug) {
			for _, c := range l.Checkpoints {
				if c.Name == "Plum Bridge" || c.Name == "Quince Turn" {
					return fmt.Errorf("%q became a stop", c.Name)
				}
			}
		}
		return nil
	})
	sc.Step(`^the car sits (.+)$`, func(where string) error {
		fx, ok := routingFixes[where]
		if !ok {
			return fmt.Errorf("no position named %q", where)
		}
		w.fix, w.have = fx, true
		return nil
	})
	sc.Step(`^Navigate in Waze goes to "([^"]*)"$`, w.wazeGoesTo)
	sc.Step(`^passing it moves the link on to the next one$`, w.passingMovesOn)
	sc.Step(`^the car has arrived at "([^"]*)"$`, func(name string) error {
		for _, l := range mustLegs(w.t, w.app, w.slug) {
			for _, c := range l.Checkpoints {
				if c.Name == name {
					return w.app.store.SetArrived(c.ID, time.Now(), nil)
				}
			}
		}
		return fmt.Errorf("no stop named %q", name)
	})
	sc.Step(`^the trip-keeper adds the routing point "([^"]*)" to day (\d) after stop (\d)$`, func(name string, day, after int) {
		slug := map[int]string{1: "day-1-ridge", 2: "day-2"}[day]
		// The place is on the invented day 2 road: a vertex, so it is a real point on the line.
		w.last = postForm(w.t, w.app, "/driver/routing", url.Values{
			"leg": {slug}, "after": {fmt.Sprint(after)}, "name": {name}, "lat": {"42.5"}, "lng": {"-103"},
		})
	})
	sc.Step(`^the routing point is added$`, func() error {
		if w.last.Code != http.StatusOK && w.last.Code != http.StatusSeeOther {
			return fmt.Errorf("answered %d: %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the routing point is refused$`, func() error {
		if w.last.Code != http.StatusConflict {
			return fmt.Errorf("answered %d, want 409: %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the document is pushed again with "([^"]*)" renamed$`, func(name string) error {
		raw, err := os.ReadFile(filepath.Join("testdata", "routing-trip-fixture.geojson"))
		if err != nil {
			return err
		}
		if !strings.Contains(string(raw), name) {
			return fmt.Errorf("the fixture has no %q", name)
		}
		w.last = pushTrip(w.t, w.app, []byte(strings.ReplaceAll(string(raw), name, name+" Two")), "")
		return nil
	})
	sc.Step(`^the push is refused whole$`, func() error {
		if w.last.Code != http.StatusConflict || !strings.Contains(w.last.Body.String(), "refused") {
			return fmt.Errorf("answered %d: %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^a document is pushed with a routing point at the private start$`, func() error {
		raw, err := os.ReadFile(filepath.Join("testdata", "routing-trip-fixture.geojson"))
		if err != nil {
			return err
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		doc["features"] = append(doc["features"].([]any), map[string]any{
			"type":       "Feature",
			"properties": map[string]any{"kind": "routing", "leg": 1, "after": 1, "name": "Door Step"},
			"geometry":   map[string]any{"type": "Point", "coordinates": []float64{-100.4321, 40.1234}},
		})
		body, _ := json.Marshal(doc)
		w.last = pushTrip(w.t, w.app, body, "")
		return nil
	})
	sc.Step(`^the push is not accepted$`, func() error {
		if w.last.Code != http.StatusBadRequest {
			return fmt.Errorf("answered %d, want 400: %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
}

// hudWaze asks the real /driver/hud.json for the Waze link at the car's position.
func (w *routingWorld) hudWaze() (waze, next string, err error) {
	if !w.have {
		return "", "", fmt.Errorf("the car has no position yet")
	}
	target := fmt.Sprintf("/driver/hud.json?lat=%v&lng=%v", w.fix[0], w.fix[1])
	rec := send(w.t, w.app, "GET", target, testToken, "", nil)
	if rec.Code != http.StatusOK {
		return "", "", fmt.Errorf("hud.json answered %d: %s", rec.Code, rec.Body.String())
	}
	var h struct {
		Waze     string `json:"waze"`
		NextName string `json:"next_name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		return "", "", err
	}
	return h.Waze, h.NextName, nil
}

func (w *routingWorld) wazeGoesTo(name string) error {
	waze, _, err := w.hudWaze()
	if err != nil {
		return err
	}
	if want := "q=" + url.QueryEscape(name); !strings.Contains(waze, want) {
		return fmt.Errorf("Navigate in Waze is %q, want it to hold %q", waze, want)
	}
	return nil
}

// passingMovesOn moves the car past the place the link now points at and expects
// the link to be on the next place: past Plum Bridge it is the next stop, Birch
// Gate; past Pine Fork it is Dogwood.
func (w *routingWorld) passingMovesOn() error {
	if w.fix == routingFixes["just after Alder on the ridge road"] {
		w.fix = routingFixes["past Plum Bridge"]
		return w.wazeGoesTo("Birch Gate")
	}
	w.fix = routingFixes["on day 2 past Pine Fork"]
	return w.wazeGoesTo("Dogwood")
}
