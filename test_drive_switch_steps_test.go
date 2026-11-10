package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// initTestDriveSwitchScenarios binds features/test_drive_switch.feature.
func initTestDriveSwitchScenarios(t *testing.T, sc *godog.ScenarioContext) {
	var (
		app   *App
		kris  *http.Cookie
		pages []string
		w     *httptest.ResponseRecorder
	)
	const hoursBar = `<ol class="hours-bar"`
	push := func(slug, name string) error {
		line := [][]float64{{-76.48, 44.23}, {-76.6, 44.5}}
		stop := func(n, ord int, nm string, c []float64) map[string]any {
			return map[string]any{"type": "Feature", "geometry": map[string]any{"type": "Point", "coordinates": c},
				"properties": map[string]any{"kind": "checkpoint", "leg": 1, "order": ord, "name": nm, "precision": "exact",
					"is_terminal": n == 1, "tz": "America/Toronto"}}
		}
		doc, _ := json.Marshal(map[string]any{"type": "FeatureCollection", "name": name, "avg_mph": 60,
			"ride": map[string]any{"slug": slug, "name": name, "start_date": "2026-11-10", "mode": "CAR"},
			"features": []any{
				map[string]any{"type": "Feature", "geometry": map[string]any{"type": "LineString", "coordinates": line},
					"properties": map[string]any{"leg": 1, "day": 1, "slug": "d1", "name": "Out", "est_miles": 22.0, "mode": "CAR", "kind": "PLANNED"}},
				stop(1, 1, "Kingston", line[0]), stop(1, 2, "Parham", line[1])}})
		req := httptest.NewRequest("POST", "/driver/trip", strings.NewReader(string(doc)))
		req.Header.Set("Content-Type", "application/geo+json")
		req.AddCookie(kris)
		rec := httptest.NewRecorder()
		app.routes().ServeHTTP(rec, req)
		if rec.Code != 200 {
			return fmt.Errorf("push %s answered %d: %s", slug, rec.Code, rec.Body)
		}
		return nil
	}
	sc.Step(`^two trips are on the volume, the second named "([^"]*)"$`, func(name string) error {
		app = testApp(t)
		kris = asUser(t, app, "kris", roleAdmin)
		if err := push("first-trip", "First Trip"); err != nil {
			return err
		}
		if err := push("test-drive", name); err != nil {
			return err
		}
		return app.store.SetMeta(metaActiveRide, "first-trip")
	})
	sc.Step(`^a visitor reads "([^"]*)" and "([^"]*)"$`, func(a, b string) {
		pages = []string{doAs(t, app, nil, "GET", a, nil).Body.String(), doAs(t, app, nil, "GET", b, nil).Body.String()}
	})
	sc.Step(`^a visitor reads "([^"]*)"$`, func(p string) {
		pages = []string{doAs(t, app, nil, "GET", p, nil).Body.String()}
	})
	sc.Step(`^the trip-keeper reads "([^"]*)"$`, func(p string) {
		pages = []string{doAs(t, app, kris, "GET", p, nil).Body.String()}
	})
	none := func() error {
		for _, p := range pages {
			if strings.Contains(p, hoursBar) {
				return fmt.Errorf("found an hours timeline")
			}
		}
		return nil
	}
	sc.Step(`^neither page has an hours timeline$`, none)
	sc.Step(`^the page has no hours timeline$`, none)
	sc.Step(`^the page has an hours timeline$`, func() error {
		if !strings.Contains(pages[0], hoursBar) {
			return fmt.Errorf("no hours timeline")
		}
		return nil
	})
	sc.Step(`^the page has no trip switch$`, func() error {
		if strings.Contains(pages[0], "trip-switch") {
			return fmt.Errorf("a visitor was offered the switch")
		}
		return nil
	})
	sc.Step(`^the page offers a switch to "([^"]*)"$`, func(name string) error {
		if !strings.Contains(pages[0], `value="test-drive"`) || !strings.Contains(pages[0], name) {
			return fmt.Errorf("no switch to %s", name)
		}
		return nil
	})
	sw := func(slug, from string) error {
		req := httptest.NewRequest("POST", "/driver/ride/active", strings.NewReader(url.Values{"slug": {slug}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "text/html")
		req.Header.Set("Referer", "http://"+req.Host+from)
		req.AddCookie(kris)
		w = httptest.NewRecorder()
		app.routes().ServeHTTP(w, req)
		if w.Code != http.StatusSeeOther {
			return fmt.Errorf("switch answered %d: %s", w.Code, w.Body)
		}
		return nil
	}
	sc.Step(`^the trip-keeper switches to "[^"]*" from "([^"]*)"$`, func(from string) error { return sw("test-drive", from) })
	sc.Step(`^the trip-keeper switches back to the first trip from "([^"]*)"$`, func(from string) error { return sw("first-trip", from) })
	sc.Step(`^the answer returns to "([^"]*)"$`, func(want string) error {
		if got := w.Header().Get("Location"); got != want {
			return fmt.Errorf("went to %q", got)
		}
		return nil
	})
	shows := func() string { return doAs(t, app, nil, "GET", "/", nil).Body.String() }
	sc.Step(`^the site shows "([^"]*)"$`, func(n string) error {
		if !strings.Contains(shows(), n) {
			return fmt.Errorf("site does not show %s", n)
		}
		return nil
	})
	sc.Step(`^the site no longer shows "([^"]*)"$`, func(n string) error {
		if strings.Contains(shows(), n) {
			return fmt.Errorf("site still shows %s", n)
		}
		return nil
	})
}
