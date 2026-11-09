package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type tripSwitchWorld struct {
	t      *testing.T
	app    *App
	cookie *http.Cookie
	slugs  []string // the two rides, first one is not active at the start
	body   string
	last   *httptest.ResponseRecorder
}

func initChangeTripScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &tripSwitchWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = tripSwitchWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^a site with two trips, the second of them active$`, w.twoTrips)
	sc.Step(`^kris is signed in as an admin$`, func() { w.cookie = asUser(w.t, w.app, "kris", roleAdmin) })
	sc.Step(`^the site is a KATE$`, func() { w.app.simAllowed = true })
	sc.Step(`^the site is not a KATE$`, func() { w.app.simAllowed = false })
	sc.Step(`^kris looks at the driver screen for a trip control$`, func() {
		w.body = doAs(w.t, w.app, w.cookie, "GET", "/driver", nil).Body.String()
	})
	sc.Step(`^the trip control offers both trips$`, w.offersBoth)
	sc.Step(`^the active trip is the selected one$`, w.activeSelected)
	sc.Step(`^there is no trip control$`, func() error {
		if strings.Contains(w.body, `action="/driver/ride/active"`) {
			return fmt.Errorf("the driver screen carries a trip control off a KATE")
		}
		return nil
	})
	sc.Step(`^kris chooses the first trip from the browser$`, func() {
		req := httptest.NewRequest("POST", "/driver/ride/active", strings.NewReader(url.Values{"slug": {w.slugs[0]}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		req.AddCookie(w.cookie)
		w.last = httptest.NewRecorder()
		w.app.routes().ServeHTTP(w.last, req)
	})
	sc.Step(`^the browser is sent to the driver screen$`, func() error {
		if w.last.Code != http.StatusSeeOther || w.last.Header().Get("Location") != "/driver" {
			return fmt.Errorf("answered %d to %q", w.last.Code, w.last.Header().Get("Location"))
		}
		return nil
	})
	sc.Step(`^the first trip is the active trip$`, func() error {
		if got := w.app.activeSlug(); got != w.slugs[0] {
			return fmt.Errorf("active is %q, want %q", got, w.slugs[0])
		}
		return nil
	})
	sc.Step(`^a script posts the first trip as the active trip$`, func() {
		w.last = doAs(w.t, w.app, w.cookie, "POST", "/driver/ride/active", url.Values{"slug": {w.slugs[0]}})
	})
	sc.Step(`^the answer is JSON naming the first trip as active$`, func() error {
		var got map[string]any
		if err := json.Unmarshal(w.last.Body.Bytes(), &got); err != nil || w.last.Code != http.StatusOK || got["active"] != w.slugs[0] {
			return fmt.Errorf("answer %d %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
}

func (w *tripSwitchWorld) twoTrips() error {
	w.app = activeTripApp(w.t)
	rides, err := w.app.store.Rides()
	if err != nil || len(rides) < 2 {
		return fmt.Errorf("want two rides, have %d (%v)", len(rides), err)
	}
	active := w.app.activeSlug()
	for _, r := range rides {
		if r.Slug != active {
			w.slugs = append([]string{r.Slug}, w.slugs...)
		}
	}
	w.slugs = append(w.slugs, active)
	w.slugs = w.slugs[len(w.slugs)-2:]
	if w.slugs[0] == active {
		return fmt.Errorf("first trip must not be the active one")
	}
	return nil
}

func (w *tripSwitchWorld) offersBoth() error {
	for _, s := range w.slugs {
		if !strings.Contains(w.body, `<option value="`+s+`"`) {
			return fmt.Errorf("no option for %q", s)
		}
	}
	return nil
}

func (w *tripSwitchWorld) activeSelected() error {
	m := regexp.MustCompile(`<option value="([^"]*)" selected`).FindAllStringSubmatch(w.body, -1)
	if len(m) != 1 || m[0][1] != w.app.activeSlug() {
		return fmt.Errorf("selected options %v, active %q", m, w.app.activeSlug())
	}
	return nil
}
