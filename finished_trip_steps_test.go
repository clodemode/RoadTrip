package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/finished_trip_is_a_record.feature and
// features/checklist_is_gone.feature. The Messages API is the apiStub: nothing
// here reaches Anthropic.

type finishedWorld struct {
	t     *testing.T
	app   *App
	stub  *apiStub
	kris  *http.Cookie
	body  string // the last page read
	code  int
	asked *httptestRec
	pages []string
}

type httptestRec struct {
	code int
	body string
}

func initFinishedTripScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &finishedWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = finishedWorld{t: t}
		return ctx, nil
	})
	cps := func() []Checkpoint {
		rv, err := w.app.loadRide()
		if err != nil {
			t.Fatal(err)
		}
		return rideCheckpoints(rv.Legs)
	}
	get := func(c *http.Cookie, path string) string {
		rec := doAs(t, w.app, c, "GET", path, nil)
		w.code = rec.Code
		w.body = rec.Body.String()
		return w.body
	}
	sc.Step(`^a ride with a start, two stops and a finish, Chester on, music played and a post made$`, func() error {
		w.stub = newAPIStub(t, stream(events(evStart(stubModel, 150, 0, 0), evText(0, "Take the scenic road."), evEnd("end_turn", 10, 0, nil))...))
		w.app = chesterApp(t, w.stub)
		w.app.tracks = newRoadtracks(rtKey, newMO(t).srv.URL)
		return nil
	})
	sc.Step(`^Kris is signed in as the trip-keeper$`, func() error {
		w.kris = asUser(t, w.app, "kris", roleAdmin)
		legs := mustLegs(t, w.app, w.app.activeSlug())
		if err := w.app.store.CreatePost(&Post{LegID: legs[0].ID, Kind: "note", OccurredAt: utcString(time.Now()), TZ: "America/Denver", Title: "Posted from the road"}); err != nil {
			return err
		}
		rec := doAs(t, w.app, w.kris, "POST", "/driver/roadtracks/state", url.Values{"album_id": {"7"}, "album": {"Abbey Road"}, "artist": {"The Beatles"},
			"track_idx": {"0"}, "position_s": {"0.4"}, "title": {"Come Together"}, "year": {"1969"}, "started": {"1760000000001"}})
		if rec.Code != http.StatusNoContent {
			return fmt.Errorf("the state post answered %d", rec.Code)
		}
		return nil
	})
	sc.Step(`^the ride's drive has started$`, func() error { startTheDrive(t, w.app); return nil })
	sc.Step(`^the ride is finished$`, func() error {
		startTheDrive(t, w.app)
		c := cps()
		at := time.Now().Add(time.Hour)
		for _, x := range c[1 : len(c)-1] {
			if x.ArrivedAt == "" && x.SkippedAt == "" {
				if err := w.app.store.SetSkipped(x.ID, at, "missed"); err != nil {
					return err
				}
			}
		}
		if err := w.app.store.SetArrived(c[len(c)-1].ID, at, nil); err != nil {
			return err
		}
		rv, err := w.app.loadRide()
		if err != nil {
			return err
		}
		if !rideFinished(rv.Legs) {
			return fmt.Errorf("the ride is not finished")
		}
		return nil
	})
	sc.Step(`^a visitor reads the home page$`, func() error { get(nil, "/"); return nil })
	sc.Step(`^Kris reads the home page$`, func() error { get(w.kris, "/"); return nil })
	sc.Step(`^Kris reads the driver screen$`, func() error { get(w.kris, "/driver"); return nil })
	sc.Step(`^the page shows the map and the post$`, func() error {
		if !strings.Contains(w.body, `id="map"`) || !strings.Contains(w.body, "Posted from the road") {
			return fmt.Errorf("the page lacks the map or the post")
		}
		return nil
	})
	sc.Step(`^the page shows the Listen Inn history$`, func() error {
		if !strings.Contains(w.body, `id="np-toggle"`) || !strings.Contains(w.body, "Come Together") {
			return fmt.Errorf("the page lacks the Listen Inn toggle or the played track")
		}
		return nil
	})
	sc.Step(`^the page shows no now-playing chip, no drive HUD, no ETA and no Waze link$`, func() error {
		for _, bad := range []string{`id="np-chip"`, "/static/js/hud.js", "Navigate in Waze", "waze.com"} {
			if strings.Contains(w.body, bad) {
				return fmt.Errorf("the finished home page still has %q", bad)
			}
		}
		return nil
	})
	sc.Step(`^a visitor reads the tracks document with the played track in it$`, func() error {
		get(nil, "/tracks.json")
		if w.code != 200 || !strings.Contains(w.body, "Come Together") {
			return fmt.Errorf("tracks.json answered %d: %s", w.code, w.body)
		}
		return nil
	})
	sc.Step(`^the driver screen hides the ETA and the navigation links$`, func() error {
		if w.code != 200 {
			return fmt.Errorf("the driver screen answered %d", w.code)
		}
		for _, id := range []string{`id="hud-eta"`, `id="stop-navigate"`} {
			i := strings.Index(w.body, id)
			if i < 0 {
				continue // not drawn at all is hidden too
			}
			open := strings.LastIndex(w.body[:i], "<")
			closeAt := strings.Index(w.body[i:], ">")
			tag := w.body[open : i+closeAt]
			// the ETA is a gauge whose own wrapper must be hidden; the Waze link itself.
			if id == `id="stop-navigate"` && !strings.Contains(tag, `aria-disabled="true"`) {
				return fmt.Errorf("Navigate is live: %s", tag)
			}
			if id == `id="hud-eta"` {
				g := strings.LastIndex(w.body[:i], `<div class="hud-gauge"`)
				if g < 0 || !strings.Contains(w.body[g:i], "hidden") {
					return fmt.Errorf("the ETA gauge is shown")
				}
			}
		}
		return nil
	})
	sc.Step(`^Kris asks Chester a question$`, func() error {
		rec := doAs(t, w.app, w.kris, "POST", "/chester/ask", url.Values{"q": {"Where should we eat?"}})
		w.asked = &httptestRec{rec.Code, rec.Body.String()}
		return nil
	})
	sc.Step(`^Chester answers with the sentence "([^"]*)"$`, func(s string) error {
		if w.asked == nil || !strings.Contains(w.asked.body, s) || w.asked.code != http.StatusServiceUnavailable {
			return fmt.Errorf("answer %+v lacks %q", w.asked, s)
		}
		return nil
	})
	sc.Step(`^Chester gives an answer$`, func() error {
		if w.asked == nil || w.asked.code != 200 || !strings.Contains(w.asked.body, "Take the scenic road.") {
			return fmt.Errorf("no answer: %+v", w.asked)
		}
		return nil
	})
	sc.Step(`^the API stub recorded (\d+) calls?$`, func(n int) error {
		if got := len(w.stub.requests()); got != n {
			return fmt.Errorf("the stub saw %d calls, want %d", got, n)
		}
		return nil
	})

	sc.Step(`^Kris asks for "([^"]*)"$`, func(p string) error { get(w.kris, p); return nil })
	sc.Step(`^the answer is (\d+)$`, func(n int) error {
		if w.code != n {
			return fmt.Errorf("answered %d, want %d", w.code, n)
		}
		return nil
	})
	sc.Step(`^Kris reads the home page, the driver screen, the plan and the Chester page$`, func() error {
		for _, p := range []string{"/", "/driver", "/plan", "/chester"} {
			rec := doAs(t, w.app, w.kris, "GET", p, nil)
			if rec.Code != 200 {
				return fmt.Errorf("%s answered %d", p, rec.Code)
			}
			w.pages = append(w.pages, rec.Body.String())
		}
		return nil
	})
	sc.Step(`^none of them links to a checklist$`, func() error {
		for _, b := range w.pages {
			if strings.Contains(strings.ToLower(b), "checklist") {
				return fmt.Errorf("a page still mentions a checklist")
			}
		}
		return nil
	})
}
