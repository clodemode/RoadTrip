package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Kris, 2026-11-07: "Start with a Start Drive button." Until the trip's start
// carries its departure stamp, the home page and the driver screen lead with
// one large Start the drive button; once the drive has started it is gone.
func TestStartTheDriveButtonLeadsUntilTheDriveStarts(t *testing.T) {
	tt := newTT(t)
	app, kris := freeApp(t, tt)
	const btn = `class="btn btn-primary btn-lg start-drive" href="/driver/post"`
	for _, path := range []string{"/", "/driver"} {
		if body := doAs(t, app, kris, "GET", path, nil).Body.String(); !strings.Contains(body, btn) {
			t.Fatalf("%s before the start: no Start the drive button", path)
		}
	}
	if body := doAs(t, app, nil, "GET", "/", nil).Body.String(); strings.Contains(body, btn) {
		t.Fatal("a visitor who is not signed in must not see the Start the drive button")
	}
	rv, err := app.loadRide()
	if err != nil {
		t.Fatal(err)
	}
	st := tripStart(rv.Legs)
	if st == nil {
		t.Fatal("fixture has no start")
	}
	if err := app.store.SetArrived(st.ID, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/driver"} {
		if body := doAs(t, app, kris, "GET", path, nil).Body.String(); strings.Contains(body, btn) {
			t.Fatalf("%s after the start: the Start the drive button is still there", path)
		}
	}
}

func initStartButtonScenarios(sc *godog.ScenarioContext) {
	var css string
	sc.Step(`^the site's stylesheet$`, func() error {
		b, err := os.ReadFile("static/css/app.css")
		css = string(b)
		return err
	})
	sc.Step(`^the sky banner colours its links but not its buttons$`, func() error {
		if regexp.MustCompile(`(?m)^\.hero-sky a\s*\{`).MatchString(css) {
			return fmt.Errorf(".hero-sky a colours every link in the banner, buttons included")
		}
		if !strings.Contains(css, ".hero-sky a:not(.btn) { color: var(--leg-accent); }") {
			return fmt.Errorf("the banner's plain links lost their accent colour")
		}
		return nil
	})
}
