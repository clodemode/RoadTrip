package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"

	"github.com/cucumber/godog"
)

// Steps for features/round_trip_reads_round_trip.feature, on the postWorld and
// the A..D trips of start_checkin_steps_test.go and round_trip_finish_steps_test.go.
type roundTripReadsWorld struct {
	p    *postWorld
	home string
}

func initRoundTripReadsScenarios(sc *godog.ScenarioContext, p *postWorld) {
	w := &roundTripReadsWorld{p: p}
	sc.Step(`^a visitor reads the ride page$`, func() error {
		rec := doAs(p.t, p.sim.app, nil, "GET", "/", nil)
		if rec.Code != http.StatusOK {
			return fmt.Errorf("the ride page answered %d", rec.Code)
		}
		w.home = rec.Body.String()
		return nil
	})
	sc.Step(`^the hero line reads "([^"]*)"$`, func(want string) error {
		m := regexp.MustCompile(`(?s)<p class="hero-line">(.*?)</p>`).FindStringSubmatch(w.home)
		if m == nil {
			return fmt.Errorf("no hero line on the ride page")
		}
		got := strings.Join(strings.Fields(html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(m[1], ""))), " ")
		if !strings.Contains(got, want) {
			return fmt.Errorf("the hero line reads %q, not %q", got, want)
		}
		return nil
	})
	sc.Step(`^the hero carries the "([^"]*)" pill$`, func(pill string) error {
		m := regexp.MustCompile(`(?s)<p class="hero-line">(.*?)</p>`).FindStringSubmatch(w.home)
		if m == nil || !strings.Contains(m[1], `<span class="pill">`+pill+`</span>`) {
			return fmt.Errorf("the hero has no %q pill", pill)
		}
		return nil
	})
	sc.Step(`^the sign-in page description says "([^"]*)"$`, func(want string) error {
		rec := doAs(p.t, p.sim.app, nil, "GET", "/driver/login", nil)
		m := regexp.MustCompile(`<meta name="description" content="([^"]*)">`).FindStringSubmatch(rec.Body.String())
		if m == nil || !strings.Contains(html.UnescapeString(m[1]), want) {
			return fmt.Errorf("the sign-in page (%d) description is %v, not %q: %.300s", rec.Code, m, want, rec.Body.String())
		}
		return nil
	})
	sc.Step(`^the plan page says "([^"]*)"$`, func(want string) error {
		rec := doAs(p.t, p.sim.app, nil, "GET", "/plan", nil)
		if rec.Code != http.StatusOK {
			return fmt.Errorf("the plan page answered %d", rec.Code)
		}
		if !regexp.MustCompile(`(?s)id="plan-topology"[^>]*>\s*` + regexp.QuoteMeta(want) + `\s*<`).MatchString(rec.Body.String()) {
			return fmt.Errorf("the plan page does not say %q", want)
		}
		return nil
	})
	planData := func(want bool) error {
		rec := doAs(p.t, p.sim.app, nil, "GET", "/plan.json", nil)
		if rec.Code != http.StatusOK {
			return fmt.Errorf("plan.json answered %d", rec.Code)
		}
		var doc struct {
			RoundTrip *bool `json:"round_trip"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			return err
		}
		if doc.RoundTrip == nil || *doc.RoundTrip != want {
			return fmt.Errorf("plan.json round_trip is %v, want %v", doc.RoundTrip, want)
		}
		return nil
	}
	sc.Step(`^the plan data says it is a round trip$`, func() error { return planData(true) })
	sc.Step(`^the plan data says it is not a round trip$`, func() error { return planData(false) })
}
