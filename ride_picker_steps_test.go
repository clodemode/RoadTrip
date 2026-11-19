package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// initRidePickerScenarios binds features/ride_picker_past_current_next.feature.
// Three invented trips on one invented road, dated past, current and next.
func initRidePickerScenarios(t *testing.T, sc *godog.ScenarioContext) {
	var (
		app  *App
		kris *http.Cookie
		page string
	)
	push := func(slug, name, start string) error {
		line := [][]float64{{-100.0, 40.0}, {-100.0, 40.5}}
		stop := func(ord int, nm string, c []float64) map[string]any {
			return map[string]any{"type": "Feature", "geometry": map[string]any{"type": "Point", "coordinates": c},
				"properties": map[string]any{"kind": "checkpoint", "nav": "point", "leg": 1, "order": ord, "name": nm, "precision": "approx",
					"is_terminal": true, "tz": "America/Chicago"}}
		}
		doc, _ := json.Marshal(map[string]any{"type": "FeatureCollection", "name": name, "avg_mph": 60,
			"ride": map[string]any{"slug": slug, "name": name, "start_date": start, "mode": "CAR"},
			"features": []any{
				map[string]any{"type": "Feature", "geometry": map[string]any{"type": "LineString", "coordinates": line},
					"properties": map[string]any{"leg": 1, "day": 1, "slug": "d1-" + slug, "name": "Out", "est_miles": 35.0, "mode": "CAR", "kind": "PLANNED"}},
				stop(1, "Mill Start", line[0]), stop(2, "Mill End", line[1])}})
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
	sc.Step(`^the ride picker has a past trip "([^"]*)", the current trip "([^"]*)" and a next trip "([^"]*)"$`, func(past, cur, next string) error {
		app = testApp(t)
		kris = asUser(t, app, "kris", roleAdmin)
		for _, x := range [][3]string{{"kingston-run", past, "2030-05-01"}, {"hill-loop", cur, "2030-06-01"}, {"waco-day", next, "2030-07-01"}} {
			if err := push(x[0], x[1], x[2]); err != nil {
				return err
			}
		}
		return app.store.SetMeta(metaActiveRide, "hill-loop")
	})
	sc.Step(`^a ride picker visitor opens "([^"]*)"$`, func(p string) {
		page = doAs(t, app, nil, "GET", p, nil).Body.String()
	})
	sc.Step(`^the ride picker keeper opens "([^"]*)"$`, func(p string) {
		page = doAs(t, app, kris, "GET", p, nil).Body.String()
	})
	pick := func() string {
		i := strings.Index(page, `class="ride-pick"`)
		if i < 0 {
			return ""
		}
		j := strings.Index(page[i:], "</form>")
		return page[i : i+j]
	}
	sc.Step(`^the ride picker offers "([^"]*)"$`, func(name string) error {
		if !strings.Contains(pick(), ">"+name) {
			return fmt.Errorf("the picker does not offer %s: %q", name, pick())
		}
		return nil
	})
	sc.Step(`^the ride picker does not mention "([^"]*)"$`, func(name string) error {
		if strings.Contains(page, name) || strings.Contains(page, "waco-day") {
			return fmt.Errorf("the page mentions the next trip")
		}
		return nil
	})
	looking := func(kind, name string) error {
		if !strings.Contains(page, `class="looking-at"`) || !strings.Contains(page, kind) || !strings.Contains(page, "<strong>"+name+"</strong>") {
			return fmt.Errorf("no %s banner for %s", kind, name)
		}
		return nil
	}
	sc.Step(`^the ride picker page says it is a past trip named "([^"]*)"$`, func(n string) error { return looking("a past trip", n) })
	sc.Step(`^the ride picker page says it is the next trip named "([^"]*)"$`, func(n string) error { return looking("the next trip", n) })
	sc.Step(`^the ride picker page is not looking at another trip$`, func() error {
		if strings.Contains(page, `class="looking-at"`) {
			return fmt.Errorf("the page says it is looking at another trip")
		}
		return nil
	})
	sc.Step(`^the ride picker page links its legs on "([^"]*)"$`, func(slug string) error {
		if !strings.Contains(page, `/legs/d1-`+slug+`?ride=`+slug) {
			return fmt.Errorf("the leg link does not keep ?ride=%s", slug)
		}
		return nil
	})
	sc.Step(`^the site still shows "([^"]*)" to a ride picker visitor$`, func(name string) error {
		if app.activeSlug() != "hill-loop" {
			return fmt.Errorf("the active trip moved to %s", app.activeSlug())
		}
		if home := doAs(t, app, nil, "GET", "/", nil).Body.String(); strings.Contains(home, `class="looking-at"`) || !strings.Contains(home, name) {
			return fmt.Errorf("the home page is not %s", name)
		}
		return nil
	})
	sc.Step(`^the ride picker page carries no driver tools$`, func() error {
		if strings.Contains(page, `href="/driver/post"`) {
			return fmt.Errorf("a looked-at trip offered the post form")
		}
		return nil
	})
	sc.Step(`^the ride picker switch lists "([^"]*)", "([^"]*)" and "([^"]*)"$`, func(a, b, c string) error {
		i := strings.Index(page, `class="trip-switch"`)
		if i < 0 {
			return fmt.Errorf("the Driver page has no keeper switch")
		}
		form := page[i : i+strings.Index(page[i:], "</form>")]
		for _, n := range []string{a, b, c} {
			if !strings.Contains(form, ">"+n) {
				return fmt.Errorf("the switch does not list %s", n)
			}
		}
		for _, g := range []string{`label="Past"`, `label="Current"`, `label="Next"`} {
			if !strings.Contains(form, g) {
				return fmt.Errorf("the switch has no %s group", g)
			}
		}
		return nil
	})
	sc.Step(`^the trip the ride picker site shows is finished$`, func() error {
		cps := rideCheckpoints(mustLegs(t, app, app.activeSlug()))
		at := time.Now()
		for _, c := range []Checkpoint{cps[0], cps[len(cps)-1]} {
			if err := app.store.SetArrived(c.ID, at, nil); err != nil {
				return err
			}
		}
		if !app.rideIsFinished() {
			return fmt.Errorf("the trip the site shows is not finished")
		}
		return nil
	})
	groups := func(class string) string {
		i := strings.Index(page, `class="`+class+`"`)
		if i < 0 {
			return ""
		}
		form := page[i : i+strings.Index(page[i:], "</form>")]
		var got []string
		for _, part := range strings.Split(form, `<optgroup label="`)[1:] {
			got = append(got, part[:strings.Index(part, `"`)])
		}
		return strings.Join(got, ", ")
	}
	sc.Step(`^the ride picker (switch )?groups are "([^"]*)"$`, func(sw, want string) error {
		class := "ride-pick"
		if sw != "" {
			class = "trip-switch"
		}
		if got := groups(class); got != want {
			return fmt.Errorf("the %s groups are %q, want %q", class, got, want)
		}
		return nil
	})
	sc.Step(`^the ride picker page has no keeper switch$`, func() error {
		if strings.Contains(page, `class="trip-switch"`) {
			return fmt.Errorf("a visitor was offered the keeper switch")
		}
		return nil
	})
}
