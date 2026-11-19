package main

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/social_preview_route_card.feature. The shared ones (the
// app, the visitor's page, the fallback card, the tag checks) are bound in
// social_card_steps_test.go, whose world these read through three getters.

func initSocialRouteCardSteps(t *testing.T, sc *godog.ScenarioContext, getApp func() *App, getRec func() *httptest.ResponseRecorder, getVisitor func() string) {
	var (
		kept    string
		fetched *httptest.ResponseRecorder
	)
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		kept, fetched = "", nil
		return ctx, nil
	})
	imgOf := func(page string) string { return socialValue(socialTags(page), "og:image") }
	sc.Step(`^the page names the trip card as an absolute https png address under "([^"]*)"$`, func(under string) error {
		img := getVisitor()
		if !strings.HasPrefix(img, "https://example.com"+under) {
			return fmt.Errorf("og:image %q is not under %s", img, under)
		}
		p, q, _ := strings.Cut(img, "?")
		if !strings.HasSuffix(p, ".png") || !strings.HasPrefix(q, "v=") || len(q) < 4 {
			return fmt.Errorf("og:image %q does not end .png before a ?v= query", img)
		}
		return nil
	})
	sc.Step(`^the alt text says "([^"]*)"$`, func(want string) error {
		got := socialValue(socialTags(getRec().Body.String()), "og:image:alt")
		if !strings.Contains(got, want) {
			return fmt.Errorf("og:image:alt is %q, want it to contain %q", got, want)
		}
		return nil
	})
	sc.Step(`^the pages "([^"]*)" name the same trip card as that page$`, func(list string) error {
		for _, p := range strings.Split(list, ",") {
			r := doAs(t, getApp(), nil, "GET", p, nil)
			if r.Code != http.StatusOK {
				return fmt.Errorf("%s answered %d", p, r.Code)
			}
			if got := imgOf(r.Body.String()); got != getVisitor() {
				return fmt.Errorf("%s names %q, the ride page names %q", p, got, getVisitor())
			}
		}
		return nil
	})
	sc.Step(`^the page names the same trip card as the visitor's page$`, func() error {
		// The shell is read after the start is stamped, so the state differs: compare the path.
		a, _, _ := strings.Cut(getVisitor(), "?")
		b, _, _ := strings.Cut(imgOf(getRec().Body.String()), "?")
		if a == "" || !strings.Contains(a, "/og/ride/") || a != b {
			return fmt.Errorf("the shell names %q, the visitor's page names %q", b, a)
		}
		return nil
	})
	sc.Step(`^a visitor fetches the card address the page names$`, func() error {
		fetched = doAs(t, getApp(), nil, "GET", strings.TrimPrefix(getVisitor(), "https://example.com"), nil)
		return nil
	})
	sc.Step(`^the answer is 200 image/png and may be cached for a day$`, func() error {
		if fetched.Code != http.StatusOK {
			return fmt.Errorf("answered %d", fetched.Code)
		}
		if ct := fetched.Header().Get("Content-Type"); ct != "image/png" {
			return fmt.Errorf("Content-Type is %q", ct)
		}
		if cc := fetched.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
			return fmt.Errorf("Cache-Control is %q", cc)
		}
		return nil
	})
	sc.Step(`^the bytes decode to exactly 1200 by 630 and are under 300 KB$`, func() error {
		b := fetched.Body.Bytes()
		if len(b) >= 300*1024 {
			return fmt.Errorf("%d bytes, want under 307200", len(b))
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("not a PNG: %v", err)
		}
		if cfg.Width != 1200 || cfg.Height != 630 {
			return fmt.Errorf("%dx%d, want 1200x630", cfg.Width, cfg.Height)
		}
		return nil
	})
	sc.Step(`^the card address is remembered$`, func() error { kept = getVisitor(); return nil })
	sc.Step(`^the card address is the remembered one$`, func() error {
		if kept == "" || getVisitor() != kept {
			return fmt.Errorf("the address is %q, it was %q", getVisitor(), kept)
		}
		return nil
	})
	sc.Step(`^the card address is not the remembered one$`, func() error {
		if kept == "" || getVisitor() == kept {
			return fmt.Errorf("the address is still %q", getVisitor())
		}
		return nil
	})
	sc.Step(`^the car leaves the start$`, func() error {
		rv, err := getApp().loadRide()
		if err != nil {
			return err
		}
		return getApp().store.SetArrived(tripStart(rv.Legs).ID, time.Now(), nil)
	})
	sc.Step(`^a visitor fetches "([^"]*)"$`, func(p string) error {
		fetched = doAs(t, getApp(), nil, "GET", p, nil)
		return nil
	})
	sc.Step(`^the visitor is sent to sign in$`, func() error {
		if fetched.Code != http.StatusFound || !strings.HasPrefix(fetched.Header().Get("Location"), "/driver/login") {
			return fmt.Errorf("answered %d to %q", fetched.Code, fetched.Header().Get("Location"))
		}
		return nil
	})
	sc.Step(`^the card address answers 404$`, func() error {
		if fetched.Code != http.StatusNotFound {
			return fmt.Errorf("answered %d", fetched.Code)
		}
		return nil
	})
	sc.Step(`^the ride's legs carry no line$`, func() error {
		_, err := getApp().store.db.Exec(`UPDATE legs SET line_geojson='{"type":"LineString","coordinates":[]}' WHERE ride_slug=?`, fixtureSlug)
		return err
	})
	sc.Step(`^the points handed to the card for the ride hold the snapped start and none of the raw coordinates$`, func() error {
		spec, _, err := getApp().rideCard(fixtureSlug)
		if err != nil {
			return err
		}
		sawStart := false
		for _, l := range spec.Legs {
			for _, p := range l.Pts {
				if math.Abs(p[1]-fixtureStart.Lat) < 1e-9 && math.Abs(p[0]-fixtureStart.Lng) < 1e-9 {
					sawStart = true
				}
				for _, raw := range []struct{ lat, lng float64 }{{40.1234, -100.4321}, {44.3217, -106.2789}} {
					if math.Abs(p[1]-raw.lat) < 1e-6 && math.Abs(p[0]-raw.lng) < 1e-6 {
						return fmt.Errorf("the card is handed the raw point %v", p)
					}
				}
			}
		}
		if !sawStart {
			return fmt.Errorf("the card is not handed the snapped start %v", fixtureStart)
		}
		return nil
	})
}
