package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/finder_detour_cap.feature and
// features/coffee_open_at_eta.feature. TomTom and the Messages API are the
// local stubs of freelance_test.go and chester_test.go.

type finderWorld struct {
	t     *testing.T
	tt    *ttStub
	stub  *apiStub
	app   *App
	c     *http.Cookie
	dest  string
	clock string
	chest bool
	out   freePicksOut
	raw   string
}

const finderLon = "-97.88"

func initFinderScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &finderWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = finderWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^a driver heading to "([^"]*)" at "([^"]*)"$`, func(dest, clock string) error {
		w.dest, w.clock = dest, clock
		w.tt = newTT(t)
		return nil
	})
	sc.Step(`^Chester is on and will keep no picks$`, func() error { w.chest = true; return nil })
	sc.Step(`^TomTom finds "([^"]*)" for "([^"]*)" with a detour of (\d+) seconds$`, func(name, q string, secs int) error {
		w.add(q, place(name, 30.30, secs, ""))
		return nil
	})
	sc.Step(`^TomTom finds the coffee shop "([^"]*)" open until "([^"]*)" today$`, func(name, until string) error {
		h, m := 0, 0
		fmt.Sscanf(until, "%d:%d", &h, &m)
		hours := fmt.Sprintf(`,"openingHours":{"mode":"nextSevenDays","timeRanges":[{"startTime":{"date":"2026-11-07","hour":6,"minute":0},"endTime":{"date":"2026-11-07","hour":%d,"minute":%d}}]}`, h, m)
		w.add("coffee shop", place(name, 30.31, 130, hours))
		return nil
	})
	sc.Step(`^TomTom finds the coffee shop "([^"]*)" with no opening hours$`, func(name string) error {
		w.add("coffee shop", place(name, 30.32, 130, ""))
		return nil
	})
	sc.Step(`^the driver taps Find picks$`, func() error {
		if w.chest {
			w.stub = newAPIStub(t, messageJSON(`{"picks":[]}`))
		}
		w.app, w.c = picksApp(t, w.tt, w.stub)
		clockAt(w.app, w.clock)
		w.out = findPicks(t, w.app, w.c, true)
		if w.stub != nil {
			for _, r := range w.stub.requests() {
				w.raw += string(r.Body)
			}
		}
		return nil
	})
	sc.Step(`^the food picks include "([^"]*)"$`, func(name string) error {
		if !w.has("food", name) {
			return fmt.Errorf("food picks lack %q: %+v", name, w.out.Cats)
		}
		return nil
	})
	sc.Step(`^the food picks do not include "([^"]*)"$`, func(name string) error {
		if w.has("food", name) {
			return fmt.Errorf("food picks hold %q: %+v", name, w.out.Cats)
		}
		return nil
	})
	sc.Step(`^TomTom was asked with a detour cap of (\d+) seconds$`, func(n int) error {
		for _, q := range w.tt.alongReq {
			if !strings.HasSuffix(q, fmt.Sprintf("|%d", n)) {
				return fmt.Errorf("cap not sent: %s", q)
			}
		}
		return nil
	})
	sc.Step(`^the food category says no place was found within about (\d+) minutes of the route$`, func(n int) error {
		for _, c := range w.out.Cats {
			if c.Key == "food" && c.State == "empty" && strings.Contains(c.Text, fmt.Sprintf("within about %d minutes of the route", n)) {
				return nil
			}
		}
		return fmt.Errorf("food category: %+v", w.out.Cats)
	})
	sc.Step(`^the picks request names "([^"]*)"$`, func(name string) error {
		if !strings.Contains(w.raw, name) {
			return fmt.Errorf("Chester was not told about %q", name)
		}
		return nil
	})
	sc.Step(`^the picks request does not name "([^"]*)"$`, func(name string) error {
		if strings.Contains(w.raw, name) {
			return fmt.Errorf("Chester was told about %q", name)
		}
		return nil
	})
	sc.Step(`^the picks request names "([^"]*)" with "([^"]*)"$`, func(name, mark string) error {
		return w.line(name, mark, true)
	})
	sc.Step(`^the picks request names "([^"]*)" without "([^"]*)"$`, func(name, mark string) error {
		return w.line(name, mark, false)
	})
	sc.Step(`^the picks request says "([^"]*)"$`, func(s string) error {
		if !strings.Contains(w.raw, s) {
			return fmt.Errorf("Chester was not told %q", s)
		}
		return nil
	})
	sc.Step(`^TomTom was asked for opening hours$`, func() error {
		if len(w.tt.alongHours) == 0 {
			return fmt.Errorf("no along-route call")
		}
		for _, h := range w.tt.alongHours {
			if h != "nextSevenDays" {
				// only the hours categories need it; coffee must have it
				continue
			}
			return nil
		}
		return fmt.Errorf("openingHours was never asked for: %v", w.tt.alongHours)
	})
	sc.Step(`^TomTom was asked for "([^"]*)"$`, func(q string) error {
		for _, r := range w.tt.alongReq {
			if strings.HasPrefix(r, q+"|") {
				return nil
			}
		}
		return fmt.Errorf("not asked for %q: %v", q, w.tt.alongReq)
	})
}

func place(name string, lat float64, secs int, hours string) string {
	return fmt.Sprintf(`{"poi":{"name":%q%s},"address":{"municipality":"Liberty Hill","countrySubdivision":"TX"},"position":{"lat":%.2f,"lon":%s},"detourTime":%d}`,
		name, hours, lat, finderLon, secs)
}

func (w *finderWorld) add(q, res string) {
	cur := w.tt.alongBody[q]
	if cur == "" {
		w.tt.alongBody[q] = `{"results":[` + res + `]}`
		return
	}
	w.tt.alongBody[q] = strings.TrimSuffix(cur, "]}") + "," + res + "]}"
}

func (w *finderWorld) has(cat, name string) bool {
	for _, c := range w.out.Cats {
		if c.Key == cat {
			for _, p := range c.Picks {
				if p.Name == name {
					return true
				}
			}
		}
	}
	return false
}

func (w *finderWorld) line(name, mark string, want bool) error {
	// the request is JSON, so a newline inside the text is the two characters \n
	for _, l := range strings.Split(w.raw, `\n`) {
		if strings.Contains(l, name) {
			if strings.Contains(l, mark) != want {
				return fmt.Errorf("Chester's line for %q is %q, want mark %q present=%v", name, l, mark, want)
			}
			return nil
		}
	}
	return fmt.Errorf("Chester was not told about %q", name)
}
