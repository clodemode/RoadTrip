package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/plan_day_summaries.feature. They ride the marks world: the
// same invented trips, then the Plan page read the way any visitor reads it.
func initPlanDaySummaryScenarios(t *testing.T, sc *godog.ScenarioContext, m *marksWorld) {
	var plan string
	tagRE := regexp.MustCompile(`<[^>]*>`)
	words := func(s string) string { return strings.Join(strings.Fields(tagRE.ReplaceAllString(s, " ")), " ") }
	dayBlock := func(n string) (string, error) {
		re := regexp.MustCompile(`(?s)<button[^>]*class="day-row"[^>]*data-day="` + n + `".*?</button>`)
		g := re.FindString(plan)
		if g == "" {
			return "", fmt.Errorf("the Plan page has no summary for day %s", n)
		}
		return g, nil
	}
	sc.Step(`^anyone opens the Plan page$`, func() error {
		w := doAs(t, m.sim.app, nil, "GET", "/plan?embed=1", nil)
		if w.Code != http.StatusOK {
			return fmt.Errorf("the Plan page answered %d", w.Code)
		}
		plan = w.Body.String()
		return nil
	})
	sc.Step(`^day (\d) reads "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, func(n, a, b, c, d string) error {
		blk, err := dayBlock(n)
		if err != nil {
			return err
		}
		txt := words(blk)
		for _, want := range []string{a, b, c, d} {
			if !strings.Contains(txt, want) {
				return fmt.Errorf("day %s reads %q, which lacks %q", n, txt, want)
			}
		}
		return nil
	})
	sc.Step(`^day (\d) names the milestones "([^"]*)"$`, func(n, want string) error {
		blk, err := dayBlock(n)
		if err != nil {
			return err
		}
		if txt := words(blk); !strings.Contains(txt, "Milestones: "+want) {
			return fmt.Errorf("day %s reads %q, which lacks %q", n, txt, "Milestones: "+want)
		}
		return nil
	})
	sc.Step(`^day (\d) names no milestones$`, func(n string) error {
		blk, err := dayBlock(n)
		if err != nil {
			return err
		}
		if strings.Contains(blk, "day-ms") || strings.Contains(blk, "Home Base") {
			return fmt.Errorf("day %s names a milestone: %q", n, words(blk))
		}
		return nil
	})
	sc.Step(`^the Plan page draws no row of hour dots$`, func() error {
		if strings.Contains(plan, `class="hours-bar"`) {
			return fmt.Errorf("the Plan page still draws an hours bar")
		}
		return nil
	})
	sc.Step(`^the trip line reads "([^"]*)"$`, func(want string) error {
		g := regexp.MustCompile(`(?s)<p class="plan-totals.*?</p>`).FindString(plan)
		if txt := words(g); !strings.Contains(txt, want) {
			return fmt.Errorf("the trip line reads %q, which lacks %q", txt, want)
		}
		return nil
	})
	script := func(path string) string { return doAs(t, m.sim.app, nil, "GET", path, nil).Body.String() }
	sc.Step(`^the Plan map and the Ride map draw a night's end as its day badge with the town$`, func() error {
		for _, p := range []string{"/static/js/plan.js", "/static/js/map.js"} {
			js := script(p)
			for _, want := range []string{"day-pin-b", "day-pin-n"} {
				if !strings.Contains(js, want) {
					return fmt.Errorf("%s lacks %q", p, want)
				}
			}
		}
		return nil
	})
	sc.Step(`^the Plan map draws the start and the milestones as small plain dots$`, func() error {
		if js := script("/static/js/plan.js"); !strings.Contains(js, "L.circleMarker") || !strings.Contains(js, "'Milestone'") {
			return fmt.Errorf("plan.js draws no small dot for the start and the milestones")
		}
		return nil
	})
}
