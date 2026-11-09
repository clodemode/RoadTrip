package main

import (
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/arrival_time_follows_the_vouch.feature. They ride the
// start-and-check-in world and add only what it lacks: a latest stamp set
// ahead of the clock (the floor an arrival may not run under), and readers
// for "arrived now" and for the notice's wording. Nothing calls a real service.
func initArrivalTimeScenarios(sc *godog.ScenarioContext, s *scWorld) {
	sc.Step(`^the drive started at A two hours from now$`, func() error {
		return s.p.sim.app.store.SetArrived(s.p.cps[0].ID, time.Now().Add(2*time.Hour), nil)
	})
	sc.Step(`^B is arrived two hours from now$`, func() error {
		return s.p.sim.app.store.SetArrived(s.p.cps[1].ID, time.Now().Add(2*time.Hour), nil)
	})
	sc.Step(`^([ABCD]) is arrived now$`, func(which string) error {
		return nearNow(s.p.cp(strings.Index("ABCD", which)).ArrivedAt)
	})
	sc.Step(`^C is still not arrived at$`, func() error { return s.notArrived(2) })
	sc.Step(`^the notice says the post was kept at its own time and the car stayed$`, func() error {
		return s.noticeHas("Posted at its own time", "the car stays where it was")
	})
}
