package main

import (
	"fmt"
	"time"

	"github.com/cucumber/godog"
)

// initEveningPostScenarios binds
// features/evening_posts_stay_on_the_day_they_were_taken.feature.
// The trip is synthetic: invented legs on invented dates in two real zones.
func initEveningPostScenarios(sc *godog.ScenarioContext) {
	var (
		ride *Ride
		legs []Leg
		now  time.Time
	)
	central, _ := time.LoadLocation("America/Chicago")
	sc.Step(`^a two-day synthetic trip whose first leg starts Eastern and whose night stop is Central$`, func() error {
		ride = &Ride{StartDate: "2026-11-12", EndDate: "2026-11-13"}
		legs = []Leg{
			{ID: 1, TZ: "America/New_York", Date: "2026-11-12", Checkpoints: []Checkpoint{
				{ID: 10, TZ: "America/New_York"}, {ID: 11, TZ: "America/Chicago"}}},
			{ID: 2, TZ: "America/Chicago", Date: "2026-11-13", Checkpoints: []Checkpoint{
				{ID: 12, TZ: "America/Chicago"}, {ID: 13, TZ: "America/Chicago"}}},
		}
		return nil
	})
	sc.Step(`^the car has arrived at the night stop$`, func() error {
		legs[0].Checkpoints[1].ArrivedAt = "2026-11-13T02:00:00Z"
		return nil
	})
	at := func(day, h, m int) { now = time.Date(2026, 11, day, h, m, 0, 0, central).UTC() }
	sc.Step(`^a post is started at 11:02 PM Central on the evening of arrival$`, func() error { at(12, 23, 2); return nil })
	sc.Step(`^a post is started at 9:30 PM Central on the evening of arrival$`, func() error { at(12, 21, 30); return nil })
	sc.Step(`^a post is started at 6:30 AM Central the morning after$`, func() error { at(13, 6, 30); return nil })
	landsIn := func(want int64) error {
		got := defaultLeg(legs, dayOf(ride, legs, now))
		if got == nil || got.ID != want {
			id := int64(0)
			if got != nil {
				id = got.ID
			}
			return fmt.Errorf("post landed in leg %d, want leg %d", id, want)
		}
		return nil
	}
	sc.Step(`^the post lands in the arriving day's leg$`, func() error { return landsIn(1) })
	sc.Step(`^the post lands in the next day's leg$`, func() error { return landsIn(2) })
}
