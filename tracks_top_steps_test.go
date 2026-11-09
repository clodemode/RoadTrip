package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// initTracksTopScenarios binds features/tracks_top_and_post_leg.feature.
func initTracksTopScenarios(t *testing.T, sc *godog.ScenarioContext, p *postWorld) {
	var (
		app  *App
		kris *http.Cookie
	)
	page := func() string { return doAs(t, app, kris, "GET", "/driver", nil).Body.String() }
	sc.Step(`^the card library is on$`, func() error {
		app, kris = tracksApp(t, newMO(t))
		return nil
	})
	sc.Step(`^the card library is off$`, func() error {
		app = testApp(t)
		kris = asUser(t, app, "kris", roleAdmin)
		return nil
	})
	sc.Step(`^the card viewer is in the "([^"]*)" mode$`, func(mode string) error {
		doAs(t, app, kris, "POST", "/driver/freelance/mode", url.Values{"mode": {mode}})
		return nil
	})
	sc.Step(`^the card is on the driver page once, before the road section$`, func() error {
		pg := page()
		if n := strings.Count(pg, `id="rt-form"`); n != 1 {
			return fmt.Errorf("want one RoadTracks form, have %d", n)
		}
		if n := strings.Count(pg, `id="rt-player"`); n != 1 {
			return fmt.Errorf("want one player, have %d", n)
		}
		road := strings.Index(pg, `class="hud-road`)
		if road < 0 {
			return fmt.Errorf("no road section")
		}
		for _, id := range []string{`id="rt-form"`, `id="rt-list"`, `id="rt-player"`} {
			if at := strings.Index(pg, id); at > road {
				return fmt.Errorf("%s is after the road section", id)
			}
		}
		return nil
	})
	sc.Step(`^the driver page has no card$`, func() error {
		if strings.Contains(page(), `id="rt-form"`) {
			return fmt.Errorf("the card is on the page with the library off")
		}
		return nil
	})

	sc.Step(`^the active trip has two legs$`, func() error {
		var doc map[string]any
		if err := json.Unmarshal(fixtureTrip(p.t), &doc); err != nil {
			return err
		}
		doc["ride"] = map[string]any{"slug": "post-photo-trip", "name": "Two Legs", "start_date": "2030-05-01", "mode": "CAR"}
		delete(doc, "options")
		var keep []any
		for _, f := range doc["features"].([]any) {
			pr := f.(map[string]any)["properties"].(map[string]any)
			if (pr["leg"] != float64(1) && pr["leg"] != float64(2)) || pr["kind"] == "candidate" || pr["choice"] == "valley" {
				continue
			}
			delete(pr, "option")
			delete(pr, "choice")
			keep = append(keep, f)
		}
		doc["features"] = keep
		raw, _ := json.Marshal(doc)
		if w := pushTrip(p.t, p.sim.app, raw, "?activate=1"); w.Code != http.StatusOK {
			return fmt.Errorf("push: %d %s", w.Code, w.Body.String())
		}
		rv, err := p.sim.app.loadRide()
		if err != nil {
			return err
		}
		if len(rv.Legs) < 2 {
			return fmt.Errorf("want two legs, the trip has %d", len(rv.Legs))
		}
		return nil
	})
	legPost := func(legID func(rv *rideView) int64) error {
		rv, err := p.sim.app.loadRide()
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("leg_id", strconv.FormatInt(legID(rv), 10))
		_ = mw.WriteField("kind", "photo")
		fw, _ := mw.CreateFormFile("photo", "p.jpg")
		_, _ = fw.Write(jpegTaken(""))
		_ = mw.Close()
		req := httptest.NewRequest("POST", "/driver/post", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.AddCookie(p.sim.cookies["kris"])
		w := httptest.NewRecorder()
		p.sim.app.routes().ServeHTTP(w, req)
		p.last = w
		return nil
	}
	sc.Step(`^kris posts a photo carrying a leg id from another trip$`, func() error {
		return legPost(func(*rideView) int64 { return 987654321 })
	})
	sc.Step(`^kris posts a photo carrying the id of the second leg$`, func() error {
		return legPost(func(rv *rideView) int64 { return rv.Legs[1].ID })
	})
	posted := func() (int, error) {
		posts, err := p.sim.app.store.Posts("post-photo-trip", 0)
		return len(posts), err
	}
	sc.Step(`^the post is refused$`, func() error {
		if n, err := posted(); err != nil || n != 0 {
			return fmt.Errorf("want no post, have %d (%v)", n, err)
		}
		return nil
	})
	sc.Step(`^the post is accepted$`, func() error {
		if n, err := posted(); err != nil || n != 1 {
			return fmt.Errorf("want the post kept, have %d (answer %d %s)", n, p.last.Code, p.last.Body.String())
		}
		return nil
	})
	onLeg := func(i int) error {
		rv, err := p.sim.app.loadRide()
		if err != nil {
			return err
		}
		posts, err := p.sim.app.store.Posts("post-photo-trip", 0)
		if err != nil || len(posts) != 1 {
			return fmt.Errorf("want one post, have %d (%v)", len(posts), err)
		}
		if posts[0].LegID != rv.Legs[i].ID {
			return fmt.Errorf("post is on leg %d, want leg index %d (%d)", posts[0].LegID, i, rv.Legs[i].ID)
		}
		return nil
	}
	sc.Step(`^the post is on the first leg$`, func() error { return onLeg(0) })
	sc.Step(`^the post is on the second leg$`, func() error { return onLeg(1) })
}
