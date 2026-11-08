package main

import (
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

var driveTracksRe = regexp.MustCompile(`<a [^>]*id="hud-drivetracks"[^>]*>`)

// initDriveTracksScenarios binds features/drive_tracks_button.feature.
func initDriveTracksScenarios(t *testing.T, sc *godog.ScenarioContext) {
	var app *App
	var c *http.Cookie
	var mo *moStub
	var page string
	on := func(link string) error {
		mo = newMO(t)
		app, c = tracksApp(t, mo)
		app.tracks.linkBase = link
		return nil
	}
	sc.Step(`^the music library is switched on$`, func() error { return on("") })
	sc.Step(`^the music library is switched on with the phone address "([^"]*)"$`, on)
	sc.Step(`^the music library is switched off$`, func() error {
		if err := on(""); err != nil {
			return err
		}
		app.tracks = nil
		return nil
	})
	sc.Step(`^the driver opens the driver screen$`, func() error {
		w := doAs(t, app, c, "GET", "/driver", nil)
		if w.Code != http.StatusOK {
			return fmt.Errorf("driver screen answered %d", w.Code)
		}
		page = w.Body.String()
		return nil
	})
	link := func() (string, error) {
		tag := driveTracksRe.FindString(page)
		if tag == "" {
			return "", fmt.Errorf("no Drive Tracks button on the driver screen")
		}
		if !strings.Contains(page[strings.Index(page, tag):], "Drive Tracks</a>") {
			return "", fmt.Errorf("the button is not labelled Drive Tracks")
		}
		return tag, nil
	}
	sc.Step(`^the Drive Tracks button opens the library's player in a new tab$`, func() error {
		tag, err := link()
		if err != nil {
			return err
		}
		if !strings.Contains(html.UnescapeString(tag), `href="`+mo.srv.URL+`/"`) || !strings.Contains(tag, `target="_blank"`) || !strings.Contains(tag, "noopener") {
			return fmt.Errorf("wrong link: %s", tag)
		}
		return nil
	})
	sc.Step(`^the Drive Tracks button goes to "([^"]*)"$`, func(want string) error {
		tag, err := link()
		if err != nil {
			return err
		}
		if !strings.Contains(html.UnescapeString(tag), `href="`+want+`"`) {
			return fmt.Errorf("want %s in %s", want, tag)
		}
		return nil
	})
	sc.Step(`^there is no Drive Tracks button$`, func() error {
		if driveTracksRe.MatchString(page) {
			return fmt.Errorf("a Drive Tracks button with the library off")
		}
		return nil
	})
}
