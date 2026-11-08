package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/sim_steps_by_post.feature. They ride the start-and-check-
// in world (stops A to D on one meridian, 55 km apart) and read the car the
// server holds for kris, the posts it pinned and the driver screen's figures.
type stepWorld struct {
	p *postWorld
	s *scWorld
}

func initSimStepsScenarios(sc *godog.ScenarioContext, p *postWorld, s *scWorld) {
	w := &stepWorld{p: p, s: s}
	sc.Step(`^the drive has started at A with the stepping car$`, w.started)
	sc.Step(`^kris has posted (\d+) photos? since the start$`, w.posts)
	sc.Step(`^kris posts (\d+) photos$`, w.posts)
	sc.Step(`^an hour passes with no post$`, func() error { p.sim.now = p.sim.now.Add(time.Hour); return nil })
	sc.Step(`^kris's simulated car is stepping$`, w.stepping)
	sc.Step(`^kris's simulated car is at ([ACD])$`, w.carAt)
	sc.Step(`^the post is pinned halfway along the route from ([ABCD]) to ([ABCD])$`, w.pinnedHalfway)
	sc.Step(`^the post is pinned at ([ABCD])$`, w.pinnedAt)
	sc.Step(`^([ABCD]) and ([ABCD]) are arrived at$`, func(a, b string) error {
		for _, n := range []string{a, b} {
			if err := s.arrived(strings.Index("ABCD", n)); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^no stop was skipped$`, w.noneSkipped)
	sc.Step(`^the notice says ([ABCD]) was reached by the simulated car$`, func(n string) error {
		return s.noticeHas("Reached "+p.cps[strings.Index("ABCD", n)].Name, "by the simulated car")
	})
	sc.Step(`^the miles to B are about half the miles from A to B$`, w.halfMiles)
}

// started starts the drive the way the form does, on a KATE: A's photo with
// no GPS of its own, the milestone A chosen.
func (w *stepWorld) started() error {
	w.p.photo = jpegTaken("")
	if err := w.s.send(0, true, false); err != nil {
		return err
	}
	if c := w.p.last.Code; c != http.StatusSeeOther {
		return fmt.Errorf("starting the drive answered %d: %s", c, w.p.last.Body.String())
	}
	return w.s.arrived(0)
}

// posts sends n posts, each a photo with no GPS of its own, and wants each one
// taken (a redirect, not a refusal).
func (w *stepWorld) posts(n string) error {
	count, _ := strconv.Atoi(n)
	for i := 0; i < count; i++ {
		w.p.photo = jpegTaken("")
		if err := w.s.send(-1, true, false); err != nil {
			return err
		}
		if c := w.p.last.Code; c != http.StatusSeeOther {
			return fmt.Errorf("post %d of %d answered %d: %s", i+1, count, c, w.p.last.Body.String())
		}
	}
	return nil
}

func (w *stepWorld) stepping() error {
	st, ok := w.p.sim.app.sim.State("kris")
	if !ok {
		return fmt.Errorf("no simulated car is running for kris")
	}
	if !st.Step {
		return fmt.Errorf("the simulated car runs by the clock, not by posts")
	}
	return nil
}

func (w *stepWorld) carAt(name string) error {
	la, ln, _, _, _, ok := w.p.sim.app.sim.PositionAt("kris", w.p.sim.now)
	if !ok {
		return fmt.Errorf("no simulated car is running for kris")
	}
	c := w.p.cps[strings.Index("ABCD", name)]
	if km := haversineKm(la, ln, c.Lat, c.Lng); km > 1 {
		return fmt.Errorf("the car is %.1f km from %s", km, name)
	}
	return nil
}

// lastPost is the newest post of the trip.
func (w *stepWorld) lastPost() (*Post, error) {
	posts, err := w.p.sim.app.store.Posts("post-photo-trip", 0)
	if err != nil {
		return nil, err
	}
	var best *Post
	for i := range posts {
		if best == nil || posts[i].ID > best.ID {
			best = &posts[i]
		}
	}
	if best == nil || best.Lat == nil || best.Lng == nil {
		return nil, fmt.Errorf("the last post has no pin")
	}
	return best, nil
}

func (w *stepWorld) pinnedAt(name string) error {
	post, err := w.lastPost()
	if err != nil {
		return err
	}
	c := w.p.cps[strings.Index("ABCD", name)]
	if km := haversineKm(*post.Lat, *post.Lng, c.Lat, c.Lng); km > 1 {
		return fmt.Errorf("the post is pinned %.1f km from %s", km, name)
	}
	return nil
}

func (w *stepWorld) pinnedHalfway(from, to string) error {
	post, err := w.lastPost()
	if err != nil {
		return err
	}
	a, b := w.p.cps[strings.Index("ABCD", from)], w.p.cps[strings.Index("ABCD", to)]
	// The stops lie on one meridian, so the line between them is the chord.
	mLat, mLng := (a.Lat+b.Lat)/2, (a.Lng+b.Lng)/2
	if km := haversineKm(*post.Lat, *post.Lng, mLat, mLng); km > 1 {
		return fmt.Errorf("the post is pinned %.1f km from halfway between %s and %s", km, from, to)
	}
	return nil
}

func (w *stepWorld) noneSkipped() error {
	for i := range w.p.cps {
		if c := w.p.cp(i); c.SkippedAt != "" {
			return fmt.Errorf("%s was skipped as %q", c.Name, c.SkipReason)
		}
	}
	return nil
}

func (w *stepWorld) halfMiles() error {
	r := doAs(w.p.t, w.p.sim.app, w.p.sim.cookies["kris"], "GET", "/driver/hud.json", nil)
	if r.Code != http.StatusOK {
		return fmt.Errorf("hud.json answered %d", r.Code)
	}
	var hud map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &hud); err != nil {
		return err
	}
	var got float64
	if _, err := fmt.Sscanf(fmt.Sprint(hud["next_miles"]), "%f mi", &got); err != nil {
		return fmt.Errorf("next_miles is %q", hud["next_miles"])
	}
	a, b := w.p.cps[0], w.p.cps[1]
	want := simHaversine(a.Lat, a.Lng, b.Lat, b.Lng) / 2
	if got < want-2 || got > want+2 {
		return fmt.Errorf("the screen says %.0f mi to B, half of A to B is %.1f mi", got, want)
	}
	return nil
}

// While a simulated car runs, a re-pin may not store a photo's own GPS or a
// browser fix: on a KATE the car is the only position.
func TestRepinRefusesRealPointsWhileTheSimRuns(t *testing.T) {
	app := testApp(t)
	app.simAllowed = true
	app.sim = newSimSvc()
	legs := testLegs(t, app)
	lat, lng := 44.2, -76.5
	post := mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "photo", Title: "t", Lat: &lat, Lng: &lng, PinSource: pinBrowser})
	repin := func(source string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/driver/post/x/pin", strings.NewReader(url.Values{"pin_source": {source}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", strconv.FormatInt(post.ID, 10))
		app.sim.Start(app.userOf(req), simState{Corridor: simCorKGN, Step: true, StepPos: [2]float64{44.2, -76.5}})
		w := httptest.NewRecorder()
		app.postRepin(w, req)
		return w
	}
	for _, src := range []string{pinEXIF, pinBrowser} {
		w := repin(src)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "the simulated car is the only position") {
			t.Fatalf("re-pin to %s answered %d %q, want a 400 naming the car", src, w.Code, w.Body.String())
		}
	}
	if got := mustGet(t, app, post.ID); got.PinSource != pinBrowser {
		t.Fatalf("a refused re-pin changed the pin to %q", got.PinSource)
	}
	if w := repin(pinNone); w.Code != http.StatusSeeOther {
		t.Fatalf("re-pin to none answered %d, want 303", w.Code)
	}
}
