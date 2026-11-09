package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// initMenuStateScenarios binds features/menu_ride_state_and_breadcrumbs.feature.
// Everything is read from the page a request really gets: the ride state from
// the banner label, the menu from the Site nav, the strip and the breadcrumb
// from their own landmarks.
func initMenuStateScenarios(t *testing.T, sc *godog.ScenarioContext) {
	var (
		app         *App
		kris, admin *http.Cookie
		page        string
	)
	var (
		navRe   = regexp.MustCompile(`(?s)<nav class="mast-nav"[^>]*>(.*?)</nav>`)
		linkRe  = regexp.MustCompile(`(?s)<a ([^>]*)>(.*?)</a>`)
		stateRe = regexp.MustCompile(`(?s)<[^>]*class="ride-state"[^>]*>(.*?)</[a-z]+>`)
		crumbRe = regexp.MustCompile(`(?s)<nav class="crumbs"[^>]*>(.*?)</nav>`)
		h1Re    = regexp.MustCompile(`(?s)<h1[ >].*?</h1>`)
		tagRe   = regexp.MustCompile(`<[^>]*>`)
		idRe    = regexp.MustCompile(`\sid="([^"]+)"`)
		stripRe = regexp.MustCompile(`class="drive-card"`)
		cardRe  = regexp.MustCompile(`id="rt-card"`)
	)
	plain := func(s string) string {
		s = strings.NewReplacer("&rsaquo;", ">", "›", ">", "&amp;", "&", "&rarr;", "→").Replace(tagRe.ReplaceAllString(s, " "))
		return strings.Join(strings.Fields(s), " ")
	}
	open := func(c *http.Cookie, path string) {
		// A signed-in page while Active is the player shell; these scenarios read
		// the page itself, which is what the shell's frame shows.
		if c != nil {
			if strings.Contains(path, "?") {
				path += "&embed=1"
			} else {
				path += "?embed=1"
			}
		}
		w := doAs(t, app, c, "GET", path, nil)
		page = w.Body.String()
		if w.Code != 200 {
			t.Fatalf("%s answered %d", path, w.Code)
		}
	}
	rideLegs := func() []Leg {
		rv, err := app.loadRide()
		if err != nil {
			t.Fatal(err)
		}
		return rv.Legs
	}
	finish := func() Checkpoint {
		cps := rideCheckpoints(rideLegs())
		return cps[len(cps)-1]
	}
	menu := func() (labels []string, current []string) {
		m := navRe.FindStringSubmatch(page)
		if m == nil {
			return nil, nil
		}
		for _, l := range linkRe.FindAllStringSubmatch(m[1], -1) {
			label := plain(l[2])
			if label == "Sign in" {
				continue
			}
			labels = append(labels, label)
			if strings.Contains(l[1], `aria-current="page"`) {
				current = append(current, label)
			}
		}
		return
	}

	sc.Step(`^a trip with a start, two stops and a finish, a music library, and Chester switched on$`, func() error {
		app = chesterApp(t, newAPIStub(t))
		app.tracks = newRoadtracks(rtKey, newMO(t).srv.URL)
		return nil
	})
	sc.Step(`^the menu trip-keeper Kris signs in$`, func() error {
		kris = asUser(t, app, "kris", roleEditor)
		admin = asUser(t, app, "boss", roleAdmin)
		return nil
	})
	sc.Step(`^the menu site is a KATE$`, func() error { app.simAllowed = true; return nil })
	sc.Step(`^the drive is started$`, func() error {
		return app.store.SetArrived(tripStart(rideLegs()).ID, time.Now(), nil)
	})
	sc.Step(`^the drive is started by the simulated car$`, func() error {
		st := tripStart(rideLegs())
		app.startSteppingSim("kris", st)
		return app.store.SetArrived(st.ID, time.Now(), nil)
	})
	sc.Step(`^the finish is reached$`, func() error { return app.store.SetArrived(finish().ID, time.Now(), nil) })
	sc.Step(`^the finish is skipped$`, func() error { return app.store.SetSkipped(finish().ID, time.Now(), "missed") })
	sc.Step(`^Kris has a saved resume point for "([^"]*)" at (\d+) seconds$`, func(album string, s int) error {
		return app.store.SavePlayerState("kris", 42, album, "The Beatles", 1, float64(s), time.Now())
	})
	sc.Step(`^Kris opens "([^"]*)"$`, func(p string) error { open(kris, p); return nil })
	sc.Step(`^a visitor opens "([^"]*)"$`, func(p string) error { open(nil, p); return nil })

	sc.Step(`^the ride state reads "([^"]*)"$`, func(want string) error {
		open(kris, "/")
		return stateIs(page, stateRe, plain, want)
	})
	sc.Step(`^the page's ride state reads "([^"]*)"$`, func(want string) error { return stateIs(page, stateRe, plain, want) })
	sc.Step(`^Kris's menu reads "([^"]*)"$`, func(want string) error {
		open(kris, "/plan")
		got, _ := menu()
		return sameList(got, want)
	})
	sc.Step(`^an admin opens "([^"]*)"$`, func(p string) error { open(admin, p); return nil })
	sc.Step(`^the menu reads "([^"]*)"$`, func(want string) error {
		got, _ := menu()
		return sameList(got, want)
	})
	sc.Step(`^exactly one menu link is marked as the current page, "([^"]*)"$`, func(want string) error {
		_, cur := menu()
		if len(cur) != 1 || cur[0] != want {
			return fmt.Errorf("current page marks are %v, want just %q", cur, want)
		}
		return nil
	})
	sc.Step(`^the page has no Drive Tracks strip$`, func() error {
		if stripRe.MatchString(page) || strings.Contains(page, `id="hud-drivetracks"`) || strings.Contains(page, `id="rt-audio"`) {
			return fmt.Errorf("the page carries the Drive Tracks strip")
		}
		return nil
	})
	sc.Step(`^the page has no link to "([^"]*)"$`, func(href string) error {
		if strings.Contains(page, `href="`+href+`"`) {
			return fmt.Errorf("the page links to %s", href)
		}
		return nil
	})
	sc.Step(`^the page has exactly one RoadTracks search card$`, func() error {
		if n := len(cardRe.FindAllString(page, -1)); n != 1 {
			return fmt.Errorf("%d search cards", n)
		}
		for _, id := range []string{"rt-form", "rt-q", "rt-list"} {
			if !strings.Contains(page, `id="`+id+`"`) {
				return fmt.Errorf("the search card lost %s", id)
			}
		}
		return nil
	})
	sc.Step(`^no element id appears twice on the page$`, func() error {
		seen := map[string]bool{}
		for _, m := range idRe.FindAllStringSubmatch(page, -1) {
			if seen[m[1]] {
				return fmt.Errorf("id %q appears twice", m[1])
			}
			seen[m[1]] = true
		}
		return nil
	})
	sc.Step(`^the page has exactly one breadcrumb and exactly one h1$`, func() error {
		if n := len(crumbRe.FindAllString(page, -1)); n != 1 {
			return fmt.Errorf("%d breadcrumbs", n)
		}
		if strings.Contains(page, `class="crumb"`) {
			return fmt.Errorf("an old <p class=crumb> is still on the page")
		}
		if n := len(h1Re.FindAllString(page, -1)); n != 1 {
			return fmt.Errorf("%d h1 elements", n)
		}
		return nil
	})
	sc.Step(`^the breadcrumb reads "([^"]*)"$`, func(want string) error {
		m := crumbRe.FindStringSubmatch(page)
		if m == nil {
			return fmt.Errorf("no breadcrumb")
		}
		if got := plain(m[1]); got != want {
			return fmt.Errorf("breadcrumb %q, want %q", got, want)
		}
		return nil
	})
	sc.Step(`^the h1 reads "([^"]*)"$`, func(want string) error {
		if got := plain(h1Re.FindString(page)); got != want {
			return fmt.Errorf("h1 %q, want %q", got, want)
		}
		return nil
	})
}

func stateIs(page string, re *regexp.Regexp, plain func(string) string, want string) error {
	m := re.FindStringSubmatch(page)
	if m == nil {
		return fmt.Errorf("the page carries no ride state")
	}
	if got := plain(m[1]); got != want {
		return fmt.Errorf("ride state %q, want %q", got, want)
	}
	return nil
}

func sameList(got []string, want string) error {
	if strings.Join(got, ", ") != want {
		return fmt.Errorf("menu is %q, want %q", strings.Join(got, ", "), want)
	}
	return nil
}

// startTheDrive stamps the active trip's start, so the ride reads Active.
func startTheDrive(t *testing.T, app *App) {
	t.Helper()
	rv, err := app.loadRide()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetArrived(tripStart(rv.Legs).ID, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
}
