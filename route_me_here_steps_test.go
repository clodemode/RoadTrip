package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/route_me_here.feature. The Messages API is the apiStub
// and TomTom is the ttStub: nothing here reaches either. The page's button is
// checked as JS text, the way chester_music_steps_test.go checks Play.

type routeWorld struct {
	t       *testing.T
	script  []http.HandlerFunc
	stub    *apiStub
	tt      *ttStub
	app     *App
	cookie  *http.Cookie
	planRaw string
	events  []chesterEvent
	resp    *httptest.ResponseRecorder
	driver  string
}

func initRouteMeHereScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &routeWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = routeWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^Chester will name the place "([^"]*)" in "([^"]*)"$`, w.willName)
	sc.Step(`^a trip-keeper signed in with the plan saved$`, w.signedIn)
	sc.Step(`^TomTom knows no such place$`, w.knowsNoPlace)
	sc.Step(`^the trip-keeper asks Chester for a place to eat$`, w.ask)
	sc.Step(`^the trip-keeper taps Route me here for "([^"]*)"$`, w.tap)
	sc.Step(`^the places event carries the route "([^"]*)"$`, w.routeIs)
	sc.Step(`^the page builds Route me here as a form posting to the freelance destination$`, w.pageBuilds)
	sc.Step(`^the driver is in Destined mode with a destination set$`, w.destined)
	sc.Step(`^the browser is sent back to the driver screen$`, w.redirected)
	sc.Step(`^the plan is unchanged$`, w.planSame)
	sc.Step(`^the driver is told "([^"]*)"$`, w.told)
	sc.Step(`^no destination is set$`, w.noneSet)
	sc.Step(`^the driver screen says "([^"]*)"$`, w.screenSays)
	sc.Step(`^the driver screen offers Trip$`, w.offersTrip)
	sc.Step(`^the model's requests hold no TomTom coordinate$`, w.noCoord)
}

func (w *routeWorld) knowsNoPlace() error {
	w.build()
	w.tt.geoBody = `{"results":[]}`
	return nil
}

func (w *routeWorld) willName(name, town string) error {
	in := js(map[string]any{"places": []any{map[string]any{
		"name": name, "kind": "food", "town": town, "why": "Invented.", "url": ""}}})
	w.script = append(w.script,
		stream(events(evStart(stubModel, 100, 0, 0), evThinking(0), evPlaces(1, "toolu_rmh", in), evEnd("tool_use", 30, 0, nil))...),
		stream(events(evStart(stubModel, 150, 0, 0), evText(0, "Try "+name+"."), evEnd("end_turn", 10, 0, nil))...))
	return nil
}

// build makes the app once: Chester on the stub and the FreeLance service on
// the TomTom stub, as one app, so a scenario can do both.
func (w *routeWorld) build() {
	if w.app != nil {
		return
	}
	w.stub = newAPIStub(w.t, w.script...)
	w.app = chesterApp(w.t, w.stub)
	w.tt = newTT(w.t)
	w.app.free = newFreeSvc(ttKey)
	w.app.free.ttBase = w.tt.srv.URL
	nws := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { rw.Write([]byte(`{"features":[]}`)) }))
	w.t.Cleanup(nws.Close)
	w.app.hazards = hazardSvcFor(nws, w.tt.srv, ttKey)
	w.cookie = asUser(w.t, w.app, "kris", roleAdmin)
}

func (w *routeWorld) plan() string {
	return doAs(w.t, w.app, w.cookie, "GET", "/plan.json", nil).Body.String()
}

func (w *routeWorld) signedIn() error {
	w.build()
	w.planRaw = w.plan()
	return nil
}

func (w *routeWorld) ask() error {
	w.build()
	rec := askForm(w.t, w.app, url.Values{"q": {"Where can I eat?"}})
	if rec.Code != http.StatusOK {
		return fmt.Errorf("ask: %d %s", rec.Code, rec.Body.String())
	}
	sc := bufio.NewScanner(bytes.NewReader(rec.Body.Bytes()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ev chesterEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return fmt.Errorf("a line of the answer is not JSON: %q", sc.Text())
		}
		w.events = append(w.events, ev)
	}
	return nil
}

func (w *routeWorld) tap(dest string) error {
	w.build()
	w.resp = doAs(w.t, w.app, w.cookie, "POST", "/driver/freelance/dest", url.Values{"dest": {dest}})
	return nil
}

func (w *routeWorld) routeIs(want string) error {
	for _, e := range w.events {
		if e.T == "places" {
			for _, c := range e.Places {
				if c.Route == want {
					return nil
				}
				return fmt.Errorf("the card's route is %q, want %q", c.Route, want)
			}
		}
	}
	return fmt.Errorf("no places event in the answer")
}

func (w *routeWorld) pageBuilds() error {
	js, err := os.ReadFile("static/js/chester.js")
	if err != nil {
		return err
	}
	for _, want := range []string{"'/driver/freelance/dest'", "p.route", "'Route me here'"} {
		if !strings.Contains(string(js), want) {
			return fmt.Errorf("static/js/chester.js does not build Route me here: missing %q", want)
		}
	}
	return nil
}

func (w *routeWorld) destined() error {
	st := w.app.freeSvc().get("kris")
	if !st.Have || st.Mode != modeDestined {
		return fmt.Errorf("free state: have=%v mode=%q err=%q", st.Have, st.Mode, st.Err)
	}
	return nil
}

func (w *routeWorld) redirected() error {
	if w.resp.Code != http.StatusSeeOther || w.resp.Header().Get("Location") != "/driver" {
		return fmt.Errorf("answer %d to %q, want 303 to /driver", w.resp.Code, w.resp.Header().Get("Location"))
	}
	return nil
}

func (w *routeWorld) planSame() error {
	if got := w.plan(); got != w.planRaw {
		return fmt.Errorf("the plan changed after Route me here")
	}
	return nil
}

func (w *routeWorld) told(s string) error {
	if st := w.app.freeSvc().get("kris"); !strings.Contains(st.Err, s) {
		return fmt.Errorf("FreeErr is %q, want it to hold %q", st.Err, s)
	}
	return nil
}

func (w *routeWorld) noneSet() error {
	if w.app.freeSvc().get("kris").Have {
		return fmt.Errorf("a destination is set")
	}
	return nil
}

func (w *routeWorld) screen() string {
	return doAs(w.t, w.app, w.cookie, "GET", "/driver", nil).Body.String()
}

func (w *routeWorld) screenSays(s string) error {
	if !strings.Contains(w.screen(), s) {
		return fmt.Errorf("the driver screen does not say %q", s)
	}
	return nil
}

func (w *routeWorld) offersTrip() error {
	if !strings.Contains(w.screen(), `name="mode" value="trip"`) {
		return fmt.Errorf("the driver screen has no Trip button")
	}
	return nil
}

func (w *routeWorld) noCoord() error {
	reqs := w.stub.requests()
	if len(reqs) == 0 {
		return fmt.Errorf("the model was never asked")
	}
	for i, r := range reqs {
		b := string(r.Body)
		for _, c := range []string{"30.4", "-97.9", "30.40", "97.90"} {
			if strings.Contains(b, c) {
				return fmt.Errorf("request %d holds the TomTom coordinate %q", i, c)
			}
		}
	}
	return nil
}
