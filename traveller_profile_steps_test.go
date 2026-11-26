package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/traveller_profile_and_home.feature. The Home here is
// invented: an address no one lives at and a point in an empty field of the
// synthetic loop fixture's grid.
const (
	inventedHomeAddress = "7 Invented Mill Road, Nowhere"
	inventedHomeLat     = "40.0034"
	inventedHomeLng     = "-99.9987"
	homeLoopSlug        = "home-loop-fixture"
)

// inventedHomeCell is the 0.01 degree cell the store may publish instead.
var inventedHomeCell = LatLng{Lat: 40.00, Lng: -100.00}

type profileWorld struct {
	t    *testing.T
	app  *App
	kris *http.Cookie
	lee  *http.Cookie
	last *httptest.ResponseRecorder
}

func initTravellerProfileScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &profileWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = profileWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^a site where kris is an admin and lee is a traveller$`, func() error {
		w.app = tokenApp(t, testToken)
		w.kris = asUser(t, w.app, "kris", roleAdmin)
		w.lee = asUser(t, w.app, "lee", roleEditor)
		return nil
	})
	saveHome := func(acc string) error {
		w.last = doAs(t, w.app, w.kris, "POST", "/driver/profile", url.Values{
			"display_name": {""}, "home_address": {inventedHomeAddress},
			"home_lat": {inventedHomeLat}, "home_lng": {inventedHomeLng}, "home_accuracy": {acc},
		})
		return nil
	}
	sc.Step(`^kris saves Home from the phone at the invented Home with a (\d+) m fix$`, saveHome)
	sc.Step(`^kris has saved the invented Home$`, func() error {
		if err := saveHome("20"); err != nil {
			return err
		}
		if w.last.Code != http.StatusOK {
			return fmt.Errorf("saving Home answered %d: %.300s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^kris's display name is "([^"]*)"$`, func(name string) error {
		rec := doAs(t, w.app, w.kris, "POST", "/driver/profile", url.Values{
			"display_name": {name}, "home_address": {inventedHomeAddress}})
		if rec.Code != http.StatusOK {
			return fmt.Errorf("saving the display name answered %d", rec.Code)
		}
		return nil
	})
	page := func(c *http.Cookie) (string, error) {
		rec := doAs(t, w.app, c, "GET", "/driver/profile", nil)
		if rec.Code != http.StatusOK {
			return "", fmt.Errorf("the profile page answered %d", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			return "", fmt.Errorf("the profile page may be cached: Cache-Control %q", cc)
		}
		return html.UnescapeString(rec.Body.String()), nil
	}
	sc.Step(`^kris's profile page shows the invented Home address$`, func() error {
		body, err := page(w.kris)
		if err != nil {
			return err
		}
		if !strings.Contains(body, inventedHomeAddress) {
			return fmt.Errorf("kris's profile page does not show the Home address")
		}
		return nil
	})
	sc.Step(`^kris's profile page says Home's point is set$`, func() error {
		body, err := page(w.kris)
		if err != nil {
			return err
		}
		if !strings.Contains(body, "Home's point is set.") {
			return fmt.Errorf("the profile page does not say Home's point is set")
		}
		return nil
	})
	sc.Step(`^kris's profile page says Home has no point yet$`, func() error {
		body, err := page(w.kris)
		if err != nil {
			return err
		}
		if !strings.Contains(body, "Home has no point yet") {
			return fmt.Errorf("the profile page does not say Home has no point yet")
		}
		return nil
	})
	sc.Step(`^the profile answers 400 and says the fix is not close enough$`, func() error {
		if w.last.Code != http.StatusBadRequest || !strings.Contains(w.last.Body.String(), "not close enough") {
			return fmt.Errorf("the profile answered %d: %.300s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^lee opens the profile page$`, func() error { return nil })
	sc.Step(`^lee's profile page does not show the invented Home address$`, func() error {
		body, err := page(w.lee)
		if err != nil {
			return err
		}
		if strings.Contains(body, inventedHomeAddress) {
			return fmt.Errorf("lee's profile page shows kris's Home address")
		}
		return nil
	})
	sc.Step(`^a visitor who opens the profile page is sent to sign in$`, func() error {
		rec := doAs(t, w.app, nil, "GET", "/driver/profile", nil)
		if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/driver/login") {
			return fmt.Errorf("a visitor got %d %q", rec.Code, rec.Header().Get("Location"))
		}
		return nil
	})
	put := func(c *http.Cookie, token, body string) {
		req := httptest.NewRequest("PUT", "/driver/profiles/kris", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if c != nil {
			req.AddCookie(c)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w.last = httptest.NewRecorder()
		w.app.routes().ServeHTTP(w.last, req)
	}
	sc.Step(`^lee puts a profile for kris$`, func() error {
		put(w.lee, "", `{"display_name": "Not Kris"}`)
		return nil
	})
	sc.Step(`^the push token puts a profile with a display name for kris$`, func() error {
		put(nil, testToken, `{"display_name": "Kris", "places": {"home": {"label": "Home", "address": "`+inventedHomeAddress+`", "lat": `+inventedHomeLat+`, "lng": `+inventedHomeLng+`}}}`)
		return nil
	})
	sc.Step(`^the profile put answers (\d+)$`, func(code int) error {
		if w.last.Code != code {
			return fmt.Errorf("the put answered %d, want %d: %.200s", w.last.Code, code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the profile put answers 200 and names no address$`, func() error {
		if w.last.Code != http.StatusOK {
			return fmt.Errorf("the put answered %d: %.200s", w.last.Code, w.last.Body.String())
		}
		if strings.Contains(w.last.Body.String(), "Invented") || strings.Contains(w.last.Body.String(), inventedHomeLat) {
			return fmt.Errorf("the put's answer carries the place: %s", w.last.Body.String())
		}
		return nil
	})
	push := func(query string) error {
		raw, err := os.ReadFile(filepath.Join("testdata", "home-loop-fixture.geojson"))
		if err != nil {
			return err
		}
		w.last = pushTrip(t, w.app, raw, query)
		return nil
	}
	sc.Step(`^the home loop trip is pushed$`, func() error { return push("") })
	sc.Step(`^the home loop trip is pushed and made active$`, func() error {
		if err := push("?activate=1"); err != nil {
			return err
		}
		if w.last.Code != http.StatusOK {
			return fmt.Errorf("the push answered %d: %.400s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the push answers 400 naming the place "([^"]*)"$`, func(key string) error {
		if w.last.Code != http.StatusBadRequest || !strings.Contains(w.last.Body.String(), `"place": "`+key+`"`) {
			return fmt.Errorf("the push answered %d: %.300s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	ends := func() ([]Checkpoint, error) {
		legs, err := w.app.store.Legs(homeLoopSlug)
		if err != nil {
			return nil, err
		}
		cps := rideCheckpoints(legs)
		if len(cps) < 2 {
			return nil, fmt.Errorf("the ride has %d stops", len(cps))
		}
		return []Checkpoint{cps[0], cps[len(cps)-1]}, nil
	}
	sc.Step(`^the trip's start and finish are named "([^"]*)"$`, func(name string) error {
		cps, err := ends()
		if err != nil {
			return err
		}
		for _, c := range cps {
			if c.Name != name {
				return fmt.Errorf("a stop is named %q, not %q", c.Name, name)
			}
		}
		return nil
	})
	sc.Step(`^both sit on the fuzzed cell of the invented Home$`, func() error {
		cps, err := ends()
		if err != nil {
			return err
		}
		for _, c := range cps {
			if c.Lat != inventedHomeCell.Lat || c.Lng != inventedHomeCell.Lng || c.Precision != precisionFuzzed {
				return fmt.Errorf("%s sits at %v,%v (%s), not on the fuzzed cell", c.Name, c.Lat, c.Lng, c.Precision)
			}
		}
		return nil
	})
	leaks := func(body string) error {
		for _, s := range []string{inventedHomeAddress, url.QueryEscape(inventedHomeAddress), inventedHomeLat, inventedHomeLng} {
			if strings.Contains(body, s) {
				return fmt.Errorf("it carries %q", s)
			}
		}
		return nil
	}
	sc.Step(`^neither the trip file nor any public page carries the invented Home$`, func() error {
		raw, err := os.ReadFile(tripPath(w.app.dataDir, homeLoopSlug))
		if err != nil {
			return err
		}
		if err := leaks(string(raw)); err != nil {
			return fmt.Errorf("the trip file: %w", err)
		}
		for _, p := range []string{"/", "/plan", "/plan.json", "/photos", "/legs/day-1-loop"} {
			rec := doAs(t, w.app, nil, "GET", p, nil)
			if err := leaks(rec.Body.String()); err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
		}
		return nil
	})
	sc.Step(`^kris's readiness page sends the finish to the invented Home address$`, func() error {
		rec := doAs(t, w.app, w.kris, "GET", "/driver/ready", nil)
		body := html.UnescapeString(rec.Body.String())
		if rec.Code != http.StatusOK || !strings.Contains(body, "Navigate goes to "+inventedHomeAddress) {
			return fmt.Errorf("the readiness page (%d) does not send the finish to Home", rec.Code)
		}
		return nil
	})
	sc.Step(`^a visitor's ride page does not carry the invented Home address$`, func() error {
		rec := doAs(t, w.app, nil, "GET", "/", nil)
		return leaks(rec.Body.String())
	})
	sc.Step(`^the ride's owner is kris$`, func() error {
		if o, _ := w.app.store.Meta("ride_owner:" + homeLoopSlug); o != "kris" {
			return fmt.Errorf("the ride's owner is %q", o)
		}
		var body struct {
			Owner string `json:"owner"`
		}
		if err := json.Unmarshal(w.last.Body.Bytes(), &body); err != nil || body.Owner != "kris" {
			return fmt.Errorf("the push answer names owner %q", body.Owner)
		}
		return nil
	})
	sc.Step(`^the ride page's cast line reads "([^"]*)"$`, func(want string) error {
		rec := doAs(t, w.app, nil, "GET", "/", nil)
		m := regexp.MustCompile(`(?s)<p class="cast">(.*?)</p>`).FindStringSubmatch(rec.Body.String())
		if m == nil {
			return fmt.Errorf("the ride page (%d) has no cast line", rec.Code)
		}
		if got := html.UnescapeString(m[1]); !strings.Contains(got, want) {
			return fmt.Errorf("the cast line reads %q", got)
		}
		return nil
	})
}
