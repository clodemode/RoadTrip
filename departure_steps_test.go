package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/departure_is_caused_by_leaving.feature and
// features/round_trip_finish_needs_the_car_to_have_been_away.feature (Kris,
// 2026-11-18: a stamp is caused by the car). They ride the start-and-check-in
// world: the same four stops along a line, the same photo and post senders.
func initDepartureScenarios(sc *godog.ScenarioContext, p *postWorld, s *scWorld) {
	sc.Step(`^a photo taken at (\d\d:\d\d) with its GPS (\d+(?:\.\d+)?) km north of A$`, func(hm string, km float64) error {
		p.photo = jpegGPSAt("2026:11:07 "+hm+":00", p.cps[0].Lat+km/111.195, p.cps[0].Lng)
		return nil
	})
	sc.Step(`^the trip was opened by a Start post$`, func() error {
		p.photo = jpegGPSAt("2026:11:07 09:15:00", p.cps[0].Lat, p.cps[0].Lng)
		if err := s.send(0, true, false); err != nil {
			return err
		}
		if p.last.Code != http.StatusSeeOther {
			return fmt.Errorf("the Start post answered %d: %s", p.last.Code, p.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the trip is open and A is not departed$`, func() error {
		if p.last.Code != http.StatusSeeOther {
			return fmt.Errorf("answer %d: %s", p.last.Code, p.last.Body.String())
		}
		if !p.sim.app.tripOpened(p.sim.app.activeSlug()) {
			return fmt.Errorf("the trip was not opened")
		}
		return s.notArrived(0)
	})
	sc.Step(`^kris drops a pin 5 km north of A$`, func() error {
		w := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "POST", "/driver/pin", url.Values{
			"lat": {fmt.Sprintf("%.4f", p.cps[0].Lat+5/111.195)}, "lng": {fmt.Sprintf("%.4f", p.cps[0].Lng)}})
		if w.Code != http.StatusOK {
			return fmt.Errorf("drop pin answered %d: %s", w.Code, w.Body.String())
		}
		return nil
	})
	var screen string
	sc.Step(`^kris looks at the driver screen$`, func() error {
		w := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "GET", "/driver", nil)
		screen = w.Body.String()
		if w.Code != http.StatusOK {
			return fmt.Errorf("driver screen answered %d", w.Code)
		}
		return nil
	})
	sc.Step(`^the driver screen reads "Packing, not driving yet" and offers "Driving now"$`, func() error {
		for _, want := range []string{"Packing, not driving yet", "Driving now", `action="/driver/ride/depart"`} {
			if !strings.Contains(screen, want) {
				return fmt.Errorf("the driver screen does not say %q", want)
			}
		}
		if strings.Contains(screen, ">Start the drive<") {
			return fmt.Errorf("the driver screen still offers to start the drive")
		}
		return nil
	})
	sc.Step(`^kris taps Driving now$`, func() error {
		w := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "POST", "/driver/ride/depart", url.Values{})
		if w.Code != http.StatusSeeOther {
			return fmt.Errorf("Driving now answered %d: %s", w.Code, w.Body.String())
		}
		return nil
	})
	sc.Step(`^no stop carries an arrival to measure drive time from$`, func() error {
		rv, err := p.sim.app.loadRide()
		if err != nil {
			return err
		}
		if c := latestArrived(rv.Legs, 0); c != nil {
			return fmt.Errorf("%s carries an arrival at %s", c.Name, c.ArrivedAt)
		}
		return nil
	})
	sc.Step(`^the drive time is measured from A's departure$`, func() error {
		rv, err := p.sim.app.loadRide()
		if err != nil {
			return err
		}
		if c := latestArrived(rv.Legs, 0); c == nil || c.ID != p.cps[0].ID {
			return fmt.Errorf("drive time is anchored on %v, want A", c)
		}
		return nil
	})

	// The round trip's own steps (the trip itself is built in round_trip_finish_steps_test.go).
	sc.Step(`^the notice says "([^"]*)"$`, func(want string) error { return s.noticeHas(want) })
	sc.Step(`^the round trip's B, C and D are untouched$`, func() error {
		for _, i := range []int{1, 2, 3} {
			if c := p.cp(i); c.ArrivedAt != "" || c.SkippedAt != "" {
				return fmt.Errorf("%s carries arrived %q skipped %q", c.Name, c.ArrivedAt, c.SkippedAt)
			}
		}
		return nil
	})
	sc.Step(`^the round trip's B and C were reached on the road$`, func() error {
		for i, h := range map[int]int{1: 10, 2: 11} {
			if err := p.sim.app.store.SetArrived(p.cps[i].ID, time.Date(2026, 11, 7, h, 0, 0, 0, time.UTC), nil); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the round trip's finish D is arrived at$`, func() error { return s.arrived(3) })
	sc.Step(`^the car was seen 30 km from the start$`, func() error {
		return p.sim.app.store.AddTrailPoint(TrailPoint{RideSlug: p.sim.app.activeSlug(), LegID: p.cps[0].LegID,
			OccurredAt: "2026-11-07T10:00:00Z", TZ: p.cps[0].TZ, Lat: p.cps[0].Lat + 30/111.195, Lng: p.cps[0].Lng, Source: trailSourcePin})
	})
	sc.Step(`^the page carries the hand check-in for the finish$`, func() error {
		loc := p.last.Header().Get("Location")
		w := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "GET", loc, nil)
		want := fmt.Sprintf(`action="/driver/checkpoint/%d/arrive"`, p.cps[3].ID)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want) {
			return fmt.Errorf("the post page (%d) does not carry %s", w.Code, want)
		}
		return nil
	})
	sc.Step(`^kris checks in at the round trip's finish by hand$`, func() error {
		w := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "POST", fmt.Sprintf("/driver/checkpoint/%d/arrive", p.cps[3].ID), url.Values{})
		if w.Code != http.StatusSeeOther {
			return fmt.Errorf("the hand check-in answered %d: %s", w.Code, w.Body.String())
		}
		return nil
	})
	sc.Step(`^the round trip is finished$`, func() error {
		if !p.sim.app.rideIsFinished() {
			return fmt.Errorf("the ride is not finished")
		}
		return nil
	})
}
