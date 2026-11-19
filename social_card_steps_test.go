package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/social_preview_fallback_card.feature.

type socialTag struct{ key, val string }

var socialTagRE = regexp.MustCompile(`<meta (?:property|name)="((?:og|twitter):[a-z:_]+)" content="([^"]*)">`)

// socialTags reads the og: and twitter: tags of a page, in page order.
func socialTags(page string) []socialTag {
	var out []socialTag
	for _, m := range socialTagRE.FindAllStringSubmatch(page, -1) {
		out = append(out, socialTag{m[1], m[2]})
	}
	return out
}

func socialValue(tags []socialTag, key string) string {
	for _, t := range tags {
		if t.key == key {
			return t.val
		}
	}
	return ""
}

var socialOrder = []string{"og:type", "og:site_name", "og:title", "og:description", "og:url", "og:image",
	"og:image:width", "og:image:height", "og:image:type", "og:image:alt",
	"twitter:card", "twitter:title", "twitter:description", "twitter:image"}

func initSocialCardScenarios(t *testing.T, sc *godog.ScenarioContext) {
	var (
		app  *App
		kris *http.Cookie
		rec  *httptest.ResponseRecorder
		// the og:image the visitor's page named, for the route-card steps
		visitor string
	)
	page := func() string { return rec.Body.String() }
	rideName := func() string {
		rv, err := app.loadRide()
		if err != nil {
			return ""
		}
		return rv.Ride.Name
	}
	sc.Step(`^the trip is active and the site's public address is "([^"]*)"$`, func(u string) error {
		app = activeTripApp(t)
		app.siteURL = u
		kris = asUser(t, app, "kris", roleAdmin)
		return nil
	})
	sc.Step(`^the trip is active and the site has no public address$`, func() error {
		app = activeTripApp(t)
		app.siteURL = ""
		return nil
	})
	sc.Step(`^the site's visibility is set to private$`, func() error { app.sitePrivate = true; return nil })
	sc.Step(`^a visitor reads the page "([^"]*)"$`, func(p string) error {
		rec = doAs(t, app, nil, "GET", p, nil)
		if rec.Code != http.StatusOK {
			return fmt.Errorf("%s answered %d", p, rec.Code)
		}
		visitor = socialValue(socialTags(page()), "og:image")
		return nil
	})
	sc.Step(`^the driver reads the page "([^"]*)"$`, func(p string) error {
		rec = doAs(t, app, kris, "GET", p, nil)
		if rec.Code != http.StatusOK {
			return fmt.Errorf("%s answered %d", p, rec.Code)
		}
		return nil
	})
	sc.Step(`^the driver, signed in with the music library on, reads the page "([^"]*)"$`, func(p string) error {
		app.tracks = newRoadtracks(rtKey, newMO(t).srv.URL)
		rv, err := app.loadRide()
		if err != nil {
			return err
		}
		if err := app.store.SetArrived(tripStart(rv.Legs).ID, time.Now(), nil); err != nil {
			return err
		}
		rec = doAs(t, app, kris, "GET", p, nil)
		return nil
	})
	sc.Step(`^the answer is the player shell$`, func() error {
		if rec.Code != http.StatusOK || !strings.Contains(page(), `id="rt-frame"`) {
			return fmt.Errorf("not the player shell (%d)", rec.Code)
		}
		return nil
	})
	sc.Step(`^the page names the fallback card as an absolute https raster image$`, func() error {
		img := socialValue(socialTags(page()), "og:image")
		if !strings.HasPrefix(img, "https://") {
			return fmt.Errorf("og:image %q is not an absolute https address", img)
		}
		if p, _, _ := strings.Cut(img, "?"); !strings.HasSuffix(p, "/static/img/og-card.png") {
			return fmt.Errorf("og:image %q is not the fallback card", img)
		}
		return nil
	})
	sc.Step(`^the page names an absolute https raster image$`, func() error {
		img, _, _ := strings.Cut(socialValue(socialTags(page()), "og:image"), "?")
		if !strings.HasPrefix(img, "https://") || !strings.HasSuffix(img, ".png") {
			return fmt.Errorf("og:image %q is not an absolute https png", img)
		}
		return nil
	})
	sc.Step(`^the card is declared 1200 by 630 as image/png with a description$`, func() error {
		tags := socialTags(page())
		for k, want := range map[string]string{"og:image:width": "1200", "og:image:height": "630", "og:image:type": "image/png"} {
			if got := socialValue(tags, k); got != want {
				return fmt.Errorf("%s is %q, want %q", k, got, want)
			}
		}
		if socialValue(tags, "og:image:alt") == "" {
			return fmt.Errorf("og:image:alt is empty")
		}
		return nil
	})
	sc.Step(`^the page carries the Twitter large-image tags$`, func() error {
		tags := socialTags(page())
		if socialValue(tags, "twitter:card") != "summary_large_image" {
			return fmt.Errorf("twitter:card is %q", socialValue(tags, "twitter:card"))
		}
		for _, k := range []string{"twitter:title", "twitter:description", "twitter:image"} {
			if socialValue(tags, k) == "" {
				return fmt.Errorf("%s is missing or empty", k)
			}
		}
		return nil
	})
	sc.Step(`^the tags come in the one fixed order$`, func() error {
		var got []string
		for _, tg := range socialTags(page()) {
			got = append(got, tg.key)
		}
		if strings.Join(got, ",") != strings.Join(socialOrder, ",") {
			return fmt.Errorf("tags are %v, want %v", got, socialOrder)
		}
		return nil
	})
	sc.Step(`^the preview title and description do not contain the ride's name$`, func() error {
		name := rideName()
		if name == "" {
			return fmt.Errorf("the ride has no name to look for")
		}
		tags := socialTags(page())
		for _, k := range []string{"og:site_name", "og:title", "og:description", "twitter:title", "twitter:description"} {
			v := socialValue(tags, k)
			if v == "" {
				return fmt.Errorf("%s is empty", k)
			}
			if strings.Contains(v, name) {
				return fmt.Errorf("%s names the ride: %q", k, v)
			}
		}
		return nil
	})
	sc.Step(`^none of the pages "([^"]*)" names an svg as its image$`, func(list string) error {
		for _, p := range strings.Split(list, ",") {
			r := doAs(t, app, nil, "GET", p, nil)
			if r.Code != http.StatusOK {
				return fmt.Errorf("%s answered %d", p, r.Code)
			}
			img, _, _ := strings.Cut(socialValue(socialTags(r.Body.String()), "og:image"), "?")
			if img == "" || strings.HasSuffix(strings.ToLower(img), ".svg") {
				return fmt.Errorf("%s: og:image is %q", p, img)
			}
		}
		return nil
	})
	sc.Step(`^the page carries no og or twitter tag$`, func() error {
		if n := len(socialTags(page())); n != 0 {
			return fmt.Errorf("%d preview tags on a page with no public address", n)
		}
		return nil
	})
	initSocialRouteCardSteps(t, sc, func() *App { return app }, func() *httptest.ResponseRecorder { return rec }, func() string { return visitor })
}
