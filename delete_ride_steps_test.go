package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// deleteWorld drives POST /driver/ride/{slug}/delete through the real mux over
// two synthetic pushed trips: trip-fixture (active) and switch-fixture.
type deleteWorld struct {
	t       *testing.T
	app     *App
	active  string
	second  string
	cookies map[string]*http.Cookie
	who     string
	last    *httptest.ResponseRecorder
}

const deleteOverlayMark = "synthetic overlay note for the delete scenarios"

func initDeleteRideScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &deleteWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = deleteWorld{t: t, cookies: map[string]*http.Cookie{}}
		return ctx, nil
	})
	sc.Step(`^a site whose active trip is pushed$`, func() error {
		w.app = activeTripApp(w.t)
		w.active = fixtureSlug
		return nil
	})
	sc.Step(`^a second pushed trip with a private block$`, func() error {
		raw, err := os.ReadFile(filepath.Join("testdata", "switch-trip-fixture.geojson"))
		if err != nil {
			return err
		}
		if r := pushTrip(w.t, w.app, raw, ""); r.Code != http.StatusOK {
			return fmt.Errorf("second trip not taken: %d %s", r.Code, r.Body.String())
		}
		w.second = "switch-fixture"
		pd, err := loadPrivate(w.app.dataDir)
		if err != nil {
			return err
		}
		pd.setRide(w.second, w.app.rideSlug, map[string]privateCheckpoint{"1": {Address: "1 Invented Way", Notes: deleteOverlayMark}})
		return savePrivate(w.app.dataDir, pd)
	})
	sc.Step(`^kris is the signed-in trip-keeper$`, func() {
		w.cookies["kris"] = asUser(w.t, w.app, "kris", roleAdmin)
		w.who = "kris"
	})
	sc.Step(`^ann is a signed-in editor$`, func() {
		w.cookies["ann"] = asUser(w.t, w.app, "ann", roleEditor)
		w.who = "ann"
	})
	sc.Step(`^a checkpoint of the second trip is arrived at$`, func() error {
		legs := mustLegs(w.t, w.app, w.second)
		return w.app.store.SetArrived(legs[0].Checkpoints[0].ID, time.Date(2026, 9, 5, 15, 0, 0, 0, time.UTC), nil)
	})
	sc.Step(`^a post on the second trip$`, func() error {
		legs := mustLegs(w.t, w.app, w.second)
		return w.app.store.CreatePost(&Post{LegID: legs[0].ID, Kind: "note", Title: "left as it was", TZ: "America/Chicago",
			OccurredAt: utcString(time.Date(2026, 9, 5, 18, 0, 0, 0, time.UTC)), PinSource: pinNone})
	})
	del := func(slug string) {
		w.last = doAs(w.t, w.app, w.cookies[w.who], "POST", "/driver/ride/"+slug+"/delete", nil)
	}
	sc.Step(`^(?:kris|ann) deletes the second trip$`, func() { del(w.second) })
	sc.Step(`^kris deletes the active trip$`, func() { del(w.active) })
	sc.Step(`^kris deletes the ride that ships with the site$`, func() error {
		for s := range embeddedRideSlugs() {
			del(s)
			return nil
		}
		return fmt.Errorf("no embedded ride")
	})
	sc.Step(`^kris deletes a ride called "([^"]*)"$`, func(slug string) { del(slug) })
	sc.Step(`^the delete is answered (\d+) saying the second trip is deleted$`, func(code int) error {
		var got map[string]any
		if err := json.Unmarshal(w.last.Body.Bytes(), &got); err != nil || w.last.Code != code ||
			got["status"] != "deleted" || got["ride"] != w.second {
			return fmt.Errorf("answer %d %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the delete is answered with (\d+)$`, func(code int) error {
		if w.last.Code != code {
			return fmt.Errorf("answered %d, want %d: %s", w.last.Code, code, w.last.Body.String())
		}
		if code == http.StatusConflict && w.last.Body.Len() == 0 {
			return fmt.Errorf("a refusal must say why")
		}
		return nil
	})
	sc.Step(`^the second trip is gone from the database, the volume and the private overlay$`, func() error {
		if _, err := w.app.store.Ride(w.second); err == nil {
			return fmt.Errorf("the ride row is still there")
		}
		if n := len(mustLegs(w.t, w.app, w.second)); n != 0 {
			return fmt.Errorf("%d legs remain", n)
		}
		if _, err := os.Stat(tripPath(w.app.dataDir, w.second)); !os.IsNotExist(err) {
			return fmt.Errorf("the trip file is still on the volume (%v)", err)
		}
		if _, ok := w.app.store.Meta(metaSelectionKey(w.second)); ok {
			return fmt.Errorf("the saved selection remains")
		}
		pd, err := loadPrivate(w.app.dataDir)
		if err != nil {
			return err
		}
		if _, ok := pd.Rides[w.second]; ok {
			return fmt.Errorf("the private block remains")
		}
		return nil
	})
	sc.Step(`^the active trip is untouched$`, func() error {
		if got := w.app.activeSlug(); got != w.active {
			return fmt.Errorf("active is %q", got)
		}
		c := rideCensus(w.t, w.app, w.active)
		if c.Legs == 0 {
			return fmt.Errorf("the active ride lost its legs")
		}
		if _, err := os.Stat(tripPath(w.app.dataDir, w.active)); err != nil {
			return fmt.Errorf("the active trip file: %v", err)
		}
		return nil
	})
	sc.Step(`^the second trip is still whole$`, func() error {
		c := rideCensus(w.t, w.app, w.second)
		if c.Legs == 0 || c.Checkpoints == 0 {
			return fmt.Errorf("census %+v", c)
		}
		if _, err := os.Stat(tripPath(w.app.dataDir, w.second)); err != nil {
			return fmt.Errorf("the trip file: %v", err)
		}
		pd, _ := loadPrivate(w.app.dataDir)
		if _, ok := pd.Rides[w.second]; !ok {
			return fmt.Errorf("the private block is gone")
		}
		return nil
	})
	_ = strconv.Itoa
}
