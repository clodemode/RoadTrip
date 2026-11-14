package main

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/waze_drops_a_passed_routing_point.feature. They reuse the
// routing world (the invented routing fixture, pushed over HTTP, the push token
// as the trip-keeper) and add a loop fixture and the car's held positions. A
// held position is a trail point dropped the way a Chester ask drops one, so it
// has gone through publishPosition like every real one.

var wazePassedFixes = map[string][2]float64{
	"just after Alder on the ridge road":  {40.13, -100.44},
	"past Plum Bridge":                    {40.6, -100.68},
	"past Quince Turn":                    {41.8, -101.8},
	"just short of Plum Bridge":           {40.499, -100.599},
	"a few metres past Plum Bridge":       {40.501, -100.601},
	"far off the road beyond Quince Turn": {41.8, -101.0},
	"just after the start of the loop":    {40.002, -100.0},
	"past Mill Corner on the loop":        {40.3, -99.5},
}

type wazePassedWorld struct {
	w    *routingWorld
	page string
	n    int
}

// registerWazePassedSteps adds this feature's steps to the routing world, whose
// steps for the routing fixture and arrivals it shares.
func registerWazePassedSteps(sc *godog.ScenarioContext, rw *routingWorld) {
	p := &wazePassedWorld{w: rw}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*p = wazePassedWorld{w: rw}
		return ctx, nil
	})
	sc.Step(`^a trip whose document is a loop that ends where it began$`, func() error { return p.push("loop-trip-fixture.geojson", "loop-fixture") })
	sc.Step(`^the app holds the car (.+)$`, func(where string) error {
		fx, ok := wazePassedFixes[where]
		if !ok {
			return fmt.Errorf("no position named %q", where)
		}
		p.n++
		rw.app.dropTrailPoint(fx[0], fx[1], trailSourceChester, time.Now().Add(time.Duration(p.n)*time.Second))
		return nil
	})
	sc.Step(`^the phone sends a position from the private start's real door$`, func() error {
		p.n++
		rw.app.dropTrailPoint(40.1234, -100.4321, trailSourceChester, time.Now().Add(time.Duration(p.n)*time.Second))
		return nil
	})
	sc.Step(`^the driver reads the Ride page$`, func() error {
		rec := send(rw.t, rw.app, "GET", "/", testToken, "", nil)
		if rec.Code != http.StatusOK {
			return fmt.Errorf("the Ride page answered %d", rec.Code)
		}
		p.page = rec.Body.String()
		return nil
	})
	sc.Step(`^the Ride page Waze link goes to "([^"]*)"$`, func(name string) error {
		sec := dayEndRe.FindString(p.page)
		if sec == "" {
			return fmt.Errorf("the Ride page has no Day's end card")
		}
		for _, m := range hrefRe.FindAllStringSubmatch(sec, -1) {
			if href := html.UnescapeString(m[1]); strings.HasPrefix(href, "https://waze.com/ul") {
				if want := "q=" + url.QueryEscape(name); !strings.Contains(href, want) {
					return fmt.Errorf("the Waze link is %q, want it to hold %q", href, want)
				}
				return nil
			}
		}
		return fmt.Errorf("no Waze link on the Day's end card")
	})
	sc.Step(`^the Ride page does not name "([^"]*)"$`, func(name string) error {
		if sec := dayEndRe.FindString(p.page); strings.Contains(sec, name) {
			return fmt.Errorf("the Day's end card still names %q", name)
		}
		return nil
	})
	sc.Step(`^the Ride page offers the day's end "([^"]*)"$`, func(name string) error {
		if sec := dayEndRe.FindString(p.page); !strings.Contains(sec, "Day&#39;s end") && !strings.Contains(sec, "Day's end") || !strings.Contains(sec, name) {
			return fmt.Errorf("the Day's end card does not offer %q", name)
		}
		return nil
	})
	sc.Step(`^the Ride page shows no coordinate of the private start's real door$`, func() error {
		for _, s := range []string{"40.1234", "-100.4321", "100.4321"} {
			if strings.Contains(p.page, s) {
				return fmt.Errorf("the Ride page carries %q", s)
			}
		}
		return nil
	})
}

func (p *wazePassedWorld) push(file, slug string) error {
	p.w.app = tokenApp(p.w.t, testToken)
	p.w.slug = slug
	raw, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		return err
	}
	rec := pushTrip(p.w.t, p.w.app, raw, "?activate=1")
	if rec.Code != http.StatusOK {
		return fmt.Errorf("the document was not taken: %d %s", rec.Code, rec.Body.String())
	}
	return nil
}
