package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// initDayBadgeScenarios binds features/day_badge.feature.
func initDayBadgeScenarios(sc *godog.ScenarioContext) {
	var got dayInfo
	sc.Step(`^a trip of (\d+) days?, on its day (\d+)$`, func(total, on int) error {
		now := time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)
		legs := make([]Leg, total)
		for i := range legs {
			legs[i] = Leg{TZ: "UTC", Date: now.AddDate(0, 0, i+1-on).Format("2006-01-02")}
		}
		got = dayOf(&Ride{StartDate: legs[0].Date}, legs, now)
		return nil
	})
	pastTrip := func(cps []Checkpoint) {
		now := time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)
		date := now.AddDate(0, 0, -1).Format("2006-01-02")
		legs := []Leg{{TZ: "UTC", Date: date, Checkpoints: cps}}
		got = dayOf(&Ride{StartDate: date, EndDate: date}, legs, now)
	}
	sc.Step(`^a trip whose last day was yesterday, with its first stop reached and its finish not$`, func() error {
		pastTrip([]Checkpoint{{ArrivedAt: "2026-11-07T15:00:00Z", TZ: "UTC"}, {TZ: "UTC"}})
		return nil
	})
	sc.Step(`^a trip whose last day was yesterday, with its finish reached$`, func() error {
		pastTrip([]Checkpoint{{ArrivedAt: "2026-11-07T15:00:00Z", TZ: "UTC"}, {ArrivedAt: "2026-11-07T18:00:00Z", TZ: "UTC"}})
		return nil
	})
	sc.Step(`^the day badge reads "([^"]*)"$`, func(want string) error {
		if got.Badge != want {
			return fmt.Errorf("badge %q, want %q", got.Badge, want)
		}
		return nil
	})
	sc.Step(`^the driver screen's day gauge says Today on a one-day trip, as drawn and as refreshed$`, func() error {
		for file, want := range map[string]string{
			"templates/driver.html": `{{if eq .H.DayTotal 1}}Today{{else}}`,
			"static/js/hud.js":      `h.day_total === 1 ? 'Today'`,
		} {
			b, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if !strings.Contains(string(b), want) {
				return fmt.Errorf("%s has no %q", file, want)
			}
		}
		return nil
	})
}
