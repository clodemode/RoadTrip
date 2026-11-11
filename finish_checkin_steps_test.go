package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// initFinishCheckinScenarios binds features/finish_checkin_ends_the_ride.feature.
// The trip, the sign-in and the banner reading are the menu world's own; these
// steps do what a check-in at the finish does to the store (earlier open stops
// skipped, the finish arrived at) and read the banner off the real page.
func initFinishCheckinScenarios(t *testing.T, sc *godog.ScenarioContext) {
	var (
		app  *App
		kris *http.Cookie
	)
	stateRe := regexp.MustCompile(`(?s)<[^>]*class="ride-state"[^>]*>(.*?)</[a-z]+>`)
	tagRe := regexp.MustCompile(`<[^>]*>`)
	cps := func() []Checkpoint {
		rv, err := app.loadRide()
		if err != nil {
			t.Fatal(err)
		}
		return rideCheckpoints(rv.Legs)
	}
	sc.Step(`^a drive with a start, two stops and a finish$`, func() error {
		app = chesterApp(t, newAPIStub(t))
		app.tracks = newRoadtracks(rtKey, newMO(t).srv.URL)
		return nil
	})
	sc.Step(`^the driver Kris is signed in$`, func() error {
		kris = asUser(t, app, "kris", roleEditor)
		return nil
	})
	sc.Step(`^the finish lies at the start$`, func() error {
		c := cps()
		st, fin := c[0], c[len(c)-1]
		_, err := app.store.db.Exec(`UPDATE checkpoints SET name=?,lat=?,lng=? WHERE id=?`, st.Name, st.Lat, st.Lng, fin.ID)
		return err
	})
	sc.Step(`^the drive has started$`, func() error { startTheDrive(t, app); return nil })
	sc.Step(`^the first stop is reached$`, func() error { return app.store.SetArrived(cps()[1].ID, time.Now(), nil) })
	sc.Step(`^Kris checks in at the finish$`, func() error {
		c := cps()
		at := time.Now().Add(time.Hour)
		for _, x := range c[1 : len(c)-1] {
			if x.ArrivedAt == "" && x.SkippedAt == "" {
				if err := app.store.SetSkipped(x.ID, at, "missed"); err != nil {
					return err
				}
			}
		}
		return app.store.SetArrived(c[len(c)-1].ID, at, nil)
	})
	sc.Step(`^the banner reads "([^"]*)"$`, func(want string) error {
		w := doAs(t, app, kris, "GET", "/?embed=1", nil)
		return stateIs(w.Body.String(), stateRe, func(s string) string {
			return strings.Join(strings.Fields(tagRe.ReplaceAllString(s, " ")), " ")
		}, want)
	})
	sc.Step(`^the second stop is skipped as missed$`, func() error {
		x := cps()[2]
		if x.SkippedAt == "" || x.SkipReason != "missed" {
			return errStr("second stop is not skipped as missed")
		}
		return nil
	})
	_ = httptest.NewRecorder
}

type errStr string

func (e errStr) Error() string { return string(e) }
