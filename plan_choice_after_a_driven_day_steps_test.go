package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// switchWorld drives the real mux over testdata/switch-trip-fixture.geojson:
// Alder to Elm, where the first day (Alder, Birch Gate, Cedar Camp) is written
// once per choice of the "tonight" option, under its own leg name, and only
// what follows it differs. Every place in it is invented.
type switchWorld struct {
	t    *testing.T
	app  *App
	slug string
	last *httptest.ResponseRecorder

	// What day 1 held before the save, to be compared with after it.
	posts   []Post
	legID   int64
	stops   map[int64]string // checkpoint id -> arrived_at
	routing []RoutingPoint
	private []byte
	stub    *apiStub
}

func initPlanChoiceAfterDrivenDayScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &switchWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = switchWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^a trip whose second night is a choice after an identical first day$`, func() error {
		w.app = tokenApp(w.t, testToken)
		w.slug = "switch-fixture"
		raw, err := os.ReadFile(filepath.Join("testdata", "switch-trip-fixture.geojson"))
		if err != nil {
			return err
		}
		w.last = pushTrip(w.t, w.app, raw, "?activate=1")
		if w.last.Code != http.StatusOK {
			return fmt.Errorf("the document was not taken: %d %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^day 1 has been driven to its last stop and holds 2 pinned posts$`, func() error {
		legs := mustLegs(w.t, w.app, w.slug)
		for _, c := range legs[0].Checkpoints {
			if err := w.app.store.SetArrived(c.ID, time.Now(), nil); err != nil {
				return err
			}
		}
		if err := w.postsOn(legs[0], 0, 2); err != nil {
			return err
		}
		return w.snapshot()
	})
	sc.Step(`^only the middle stop of day 1 has been reached and holds a post$`, func() error {
		legs := mustLegs(w.t, w.app, w.slug)
		mid := legs[0].Checkpoints[1]
		if err := w.app.store.SetArrived(mid.ID, time.Now(), nil); err != nil {
			return err
		}
		if err := w.postsOn(legs[0], mid.ID, 1); err != nil {
			return err
		}
		return w.snapshot()
	})
	sc.Step(`^the trip-keeper saves the second night as "([^"]*)"$`, func(c string) {
		w.last = postForm(w.t, w.app, "/driver/plan", url.Values{"opt-tonight": {c}})
	})
	sc.Step(`^the second-night save is taken$`, func() error {
		if w.last.Code != http.StatusOK {
			return fmt.Errorf("the save answered %d: %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the second-night save is refused saying "([^"]*)"$`, func(words string) error {
		if w.last.Code != http.StatusConflict || !strings.Contains(w.last.Body.String(), words) {
			return fmt.Errorf("the save answered %d, want 409 saying %q: %s", w.last.Code, words, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^day 2 ends at "([^"]*)" and day 3 starts there$`, func(name string) error {
		if err := w.day2Ends(name); err != nil {
			return err
		}
		legs := mustLegs(w.t, w.app, w.slug)
		if want := name + " → Elm"; legs[2].Name != want {
			return fmt.Errorf("day 3 is %q, want %q", legs[2].Name, want)
		}
		return nil
	})
	sc.Step(`^day 2 still ends at "([^"]*)"$`, w.day2Ends)
	sc.Step(`^the saved selection has the second night "([^"]*)"$`, func(c string) error {
		sel := w.app.store.StoredSelection(w.slug)
		if sel == nil || sel.Choices["tonight"] != c {
			return fmt.Errorf("the saved selection is %+v, want tonight=%s", sel, c)
		}
		return nil
	})
	sc.Step(`^day 1's posts are the same rows with the same pins and words$`, func() error {
		now, err := w.app.store.Posts(w.slug, w.legID)
		if err != nil {
			return err
		}
		if len(now) != len(w.posts) {
			return fmt.Errorf("day 1 holds %d posts, held %d", len(now), len(w.posts))
		}
		for i := range now {
			// The leg's slug is the one thing the save is meant to rename: it names
			// the choice the document wrote the day for. Every stored column stays.
			a, b := now[i], w.posts[i]
			a.LegSlug, b.LegSlug = "", ""
			if !reflect.DeepEqual(a, b) {
				return fmt.Errorf("post %d changed: was %+v, is %+v", w.posts[i].ID, w.posts[i], now[i])
			}
		}
		return nil
	})
	sc.Step(`^day 1's stops are the same rows, each arrived at the same moment$`, func() error {
		legs := mustLegs(w.t, w.app, w.slug)
		if len(legs[0].Checkpoints) != len(w.stops) {
			return fmt.Errorf("day 1 has %d stops, had %d", len(legs[0].Checkpoints), len(w.stops))
		}
		for _, c := range legs[0].Checkpoints {
			if at, ok := w.stops[c.ID]; !ok || at != c.ArrivedAt {
				return fmt.Errorf("stop %d is arrived at %q, was %q (known row %v)", c.Ord, c.ArrivedAt, at, ok)
			}
		}
		return nil
	})
	sc.Step(`^day 1's leg is the same row with its routing points on the same rows$`, func() error {
		legs := mustLegs(w.t, w.app, w.slug)
		if legs[0].ID != w.legID {
			return fmt.Errorf("day 1 is row %d, was row %d", legs[0].ID, w.legID)
		}
		rp, err := w.app.store.RoutingByLeg(w.legID)
		if err != nil {
			return err
		}
		if len(w.routing) == 0 || !reflect.DeepEqual(rp, w.routing) {
			return fmt.Errorf("routing points are %+v, were %+v", rp, w.routing)
		}
		return nil
	})
	sc.Step(`^the private note on a day 1 stop is as it was$`, func() error {
		now, err := os.ReadFile(filepath.Join(w.app.dataDir, privateFile))
		if err != nil || string(now) != string(w.private) {
			return fmt.Errorf("the private file changed (%v)", err)
		}
		pd, err := loadPrivate(w.app.dataDir)
		if err != nil {
			return err
		}
		if pd.forRide(w.slug, w.app.rideSlug).Checkpoints["3"].Notes != "a note nobody else reads" {
			return fmt.Errorf("the note on stop 3 is gone")
		}
		return nil
	})
	sc.Step(`^Chester is asked about the second night with the model stubbed$`, func() error {
		w.stub = newAPIStub(w.t, stream(events(evStart(stubModel, 150, 0, 0), evText(0, "Stay with the plan."), evEnd("end_turn", 10, 0, nil))...))
		w.app.chester = newChester(chesterEnv(w.t, w.stub, nil))
		rec := askForm(w.t, w.app, url.Values{"q": {"Where should we sleep on night 2?"}})
		_ = rec
		return nil
	})
	sc.Step(`^Chester's route choices call "([^"]*)" open and "([^"]*)" and "([^"]*)" LOCKED$`, func(open, a, b string) error {
		block := w.block()
		if block == "" {
			return fmt.Errorf("the model was told no ROUTE CHOICES (%d requests)", len(w.stub.requests()))
		}
		line := func(id string) string {
			for _, l := range strings.Split(block, "\n") {
				if strings.Contains(l, "["+id+"]") {
					return l
				}
			}
			return ""
		}
		if l := line(open); l == "" || strings.Contains(l, "LOCKED") {
			return fmt.Errorf("the line for %q is %q, want it open", open, l)
		}
		for _, id := range []string{a, b} {
			if l := line(id); !strings.Contains(l, "LOCKED") {
				return fmt.Errorf("the line for %q is %q, want it LOCKED", id, l)
			}
		}
		return nil
	})
}

// postsOn makes n pinned posts on the leg, on the stop when one is named.
func (w *switchWorld) postsOn(l Leg, cpID int64, n int) error {
	for i := 0; i < n; i++ {
		lat, lng := 40.5+float64(i)*0.25, -100.75+float64(i)*0.125
		err := w.app.store.CreatePost(&Post{LegID: l.ID, CheckpointID: cpID, Kind: "note",
			OccurredAt: utcString(time.Now().Add(time.Duration(i) * time.Minute)), TZ: "America/Chicago",
			Title: fmt.Sprintf("on the road %d", i+1), Body: "words", Lat: &lat, Lng: &lng, PinSource: "browser"})
		if err != nil {
			return err
		}
	}
	return nil
}

// snapshot records everything day 1 holds right now, and puts a private note
// on its last stop, so the save can be compared against it.
func (w *switchWorld) snapshot() error {
	legs := mustLegs(w.t, w.app, w.slug)
	w.legID = legs[0].ID
	posts, err := w.app.store.Posts(w.slug, w.legID)
	if err != nil {
		return err
	}
	w.posts = posts
	w.stops = map[int64]string{}
	for _, c := range legs[0].Checkpoints {
		w.stops[c.ID] = c.ArrivedAt
	}
	if w.routing, err = w.app.store.RoutingByLeg(w.legID); err != nil {
		return err
	}
	pd := privateData{}
	pd.setRide(w.slug, w.app.rideSlug, map[string]privateCheckpoint{"3": {Address: "nowhere real", Notes: "a note nobody else reads"}})
	if err := savePrivate(w.app.dataDir, pd); err != nil {
		return err
	}
	w.private, err = os.ReadFile(filepath.Join(w.app.dataDir, privateFile))
	return err
}

func (w *switchWorld) day2Ends(name string) error {
	legs := mustLegs(w.t, w.app, w.slug)
	if len(legs) < 3 {
		return fmt.Errorf("the ride has %d legs", len(legs))
	}
	cps := legs[1].Checkpoints
	if got := cps[len(cps)-1].Name; got != name {
		return fmt.Errorf("day 2 ends at %q, want %q", got, name)
	}
	return nil
}

// block is the ROUTE CHOICES block as the model was given it.
func (w *switchWorld) block() string {
	reqs := w.stub.requests()
	if len(reqs) == 0 {
		return ""
	}
	var req struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	if err := json.Unmarshal(reqs[0].Body, &req); err != nil {
		return ""
	}
	for _, s := range req.System {
		if i := strings.Index(s.Text, "\n\nROUTE CHOICES."); i >= 0 {
			return s.Text[i:]
		}
	}
	return ""
}
