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
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// forkWorld drives the real mux with the push token over the fork fixture
// (testdata/fork-trip-fixture.geojson): Alder to Elm, where Night 1 is Cedar
// Camp or, a long day on, Far Hollow. Every place in it is invented.
type forkWorld struct {
	t           *testing.T
	app         *App
	slug        string
	last        *httptest.ResponseRecorder
	milesBefore float64 // the HUD's miles left just before the last save
	viewQuery   string  // the query the plan page is viewed with, "" for the saved plan
}

func initNightForkScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &forkWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = forkWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^a trip whose Night 1 is a fork between a near camp and a far hollow$`, func() error {
		w.app = tokenApp(w.t, testToken)
		w.slug = "fork-fixture"
		w.last = pushTrip(w.t, w.app, w.fixture(), "?activate=1")
		if w.last.Code != http.StatusOK {
			return fmt.Errorf("the document was not taken: %d %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the plan page offers option "([^"]*)" with choices "([^"]*)" and "([^"]*)"$`, func(opt, a, b string) error {
		body, err := w.planPage("")
		if err != nil {
			return err
		}
		for _, c := range []string{a, b} {
			if !strings.Contains(body, `name="opt-`+opt+`" value="`+c+`"`) {
				return fmt.Errorf("the plan page has no choice %q for option %q", c, opt)
			}
		}
		return w.optionOffered(body, opt, true)
	})
	sc.Step(`^the plan page (still )?offers option "([^"]*)"$`, func(_, opt string) error {
		body, err := w.planPage(w.viewQuery)
		if err != nil {
			return err
		}
		return w.optionOffered(body, opt, true)
	})
	sc.Step(`^the plan page does not offer option "([^"]*)"$`, func(opt string) error {
		body, err := w.planPage(w.viewQuery)
		if err != nil {
			return err
		}
		return w.optionOffered(body, opt, false)
	})
	sc.Step(`^the plan page is viewed with Night 1 as "([^"]*)"$`, func(c string) {
		w.viewQuery = "?opt-night1=" + c
	})
	sc.Step(`^the plan under Night 1 "([^"]*)" has (\d+) days, and day (\d+) is (long|not long)$`, func(c string, days, day int, long string) error {
		return w.planIs("opt-night1="+c, days, day, long == "long")
	})
	sc.Step(`^the plan under "([^"]*)" has (\d+) days, and day (\d+) is (long|not long)$`, func(sel string, days, day int, long string) error {
		q := []string{}
		for _, kv := range strings.Fields(sel) {
			q = append(q, "opt-"+kv)
		}
		return w.planIs(strings.Join(q, "&"), days, day, long == "long")
	})
	sc.Step(`^the drive has started from Alder$`, func() error { return w.arrive("Alder") })
	sc.Step(`^the car has reached "([^"]*)"$`, w.arrive)
	sc.Step(`^a note was posted on the road$`, func() error {
		legs := mustLegs(w.t, w.app, w.slug)
		return w.app.store.CreatePost(&Post{LegID: legs[0].ID, Kind: "note", OccurredAt: utcString(time.Now()),
			TZ: "America/Chicago", Title: "on the road"})
	})
	sc.Step(`^tonight's stop on the HUD is "([^"]*)"$`, func(name string) error {
		h, err := w.hud()
		if err != nil {
			return err
		}
		if h.Tonight != name {
			return fmt.Errorf("tonight's stop is %q, want %q", h.Tonight, name)
		}
		return nil
	})
	sc.Step(`^the HUD's Navigate in Waze goes to "([^"]*)"$`, func(name string) error {
		h, err := w.hud()
		if err != nil {
			return err
		}
		if want := "q=" + url.QueryEscape(name); !strings.Contains(h.Waze, want) {
			return fmt.Errorf("Navigate in Waze is %q, want it to hold %q", h.Waze, want)
		}
		return nil
	})
	sc.Step(`^the HUD's miles left grew$`, func() error {
		h, err := w.hud()
		if err != nil {
			return err
		}
		if !(h.MilesLeftN > w.milesBefore) {
			return fmt.Errorf("miles left is %.0f, was %.0f before the save", h.MilesLeftN, w.milesBefore)
		}
		return nil
	})
	sc.Step(`^the trip-keeper saves Night 1 as "([^"]*)"$`, func(c string) error {
		// The HUD's figure before the save, for "grew" to be measured against.
		h, err := w.hud()
		if err != nil {
			return err
		}
		w.milesBefore = h.MilesLeftN
		w.last = postForm(w.t, w.app, "/driver/plan", url.Values{"opt-night1": {c}})
		return nil
	})
	sc.Step(`^the save is accepted$`, func() error {
		if w.last.Code != http.StatusOK {
			return fmt.Errorf("the save answered %d: %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the trip-keeper saves the day-1 road as "([^"]*)"$`, func(c string) {
		w.last = postForm(w.t, w.app, "/driver/plan", url.Values{"opt-day1": {c}})
	})
	sc.Step(`^the save is refused and says that checkpoint 3 is arrived$`, func() error {
		return w.refusedSaying("checkpoint 3 is arrived")
	})
	sc.Step(`^the save is refused and says that leg 1 has been driven to its last stop$`, func() error {
		return w.refusedSaying("leg 1 holds 1 post(s) and has been driven to its last stop")
	})
	sc.Step(`^the saved plan's day 1 is (\d+) miles$`, func(miles float64) error {
		p, err := w.planJSON("")
		if err != nil {
			return err
		}
		if len(p.Plan.Days) == 0 || p.Plan.Days[0].Miles != miles {
			return fmt.Errorf("day 1 is %v miles, want %v", p.Plan.Days, miles)
		}
		return nil
	})
	sc.Step(`^"([^"]*)" is still arrived at$`, func(name string) error {
		c := w.cp(name)
		if c == nil || c.ArrivedAt == "" {
			return fmt.Errorf("%q is not arrived at", name)
		}
		return nil
	})
	sc.Step(`^the note is still there$`, func() error {
		if n := rideCensus(w.t, w.app, w.slug).Posts; n != 1 {
			return fmt.Errorf("the ride holds %d posts, want the one note", n)
		}
		return nil
	})
	sc.Step(`^the saved selection has Night 1 "([^"]*)"$`, func(c string) error {
		sel := w.app.store.StoredSelection(w.slug)
		if sel == nil || sel.Choices["night1"] != c {
			return fmt.Errorf("the saved selection is %+v, want night1=%s", sel, c)
		}
		return nil
	})
	sc.Step(`^the document is pushed again unchanged$`, func() {
		w.last = pushTrip(w.t, w.app, w.fixture(), "")
	})
	sc.Step(`^the push is taken$`, func() error {
		if w.last.Code != http.StatusOK {
			return fmt.Errorf("the push answered %d: %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the document is pushed with the day-2 option depending on option "([^"]*)"$`, func(opt string) error {
		var doc map[string]any
		if err := json.Unmarshal(w.fixture(), &doc); err != nil {
			return err
		}
		opts := doc["options"].([]any)
		opts[2].(map[string]any)["when"] = map[string]any{opt: "x"}
		body, _ := json.Marshal(doc)
		w.last = pushTrip(w.t, w.app, body, "")
		return nil
	})
	sc.Step(`^the document is pushed with a leg tied to night1 "([^"]*)"$`, func(c string) error {
		raw := strings.Replace(string(w.fixture()), `"when": {"day1": "ridge", "night1": "far"}`, `"when": {"day1": "ridge", "night1": "`+c+`"}`, 1)
		if !strings.Contains(raw, c) {
			return fmt.Errorf("the fixture was not changed")
		}
		w.last = pushTrip(w.t, w.app, []byte(raw), "")
		return nil
	})
	sc.Step(`^the push is refused as not a trip document, naming "([^"]*)"$`, func(word string) error {
		if w.last.Code != http.StatusBadRequest || !strings.Contains(w.last.Body.String(), word) {
			return fmt.Errorf("the push answered %d: %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
}

func (w *forkWorld) refusedSaying(words string) error {
	if w.last.Code != http.StatusConflict || !strings.Contains(w.last.Body.String(), words) {
		return fmt.Errorf("the save answered %d, want 409 saying %q: %s", w.last.Code, words, w.last.Body.String())
	}
	return nil
}

func (w *forkWorld) fixture() []byte {
	raw, err := os.ReadFile(filepath.Join("testdata", "fork-trip-fixture.geojson"))
	if err != nil {
		w.t.Fatalf("read fixture: %v", err)
	}
	return raw
}

func (w *forkWorld) planPage(query string) (string, error) {
	rec := send(w.t, w.app, "GET", "/plan"+query, testToken, "", nil)
	if rec.Code != http.StatusOK {
		return "", fmt.Errorf("/plan answered %d", rec.Code)
	}
	return rec.Body.String(), nil
}

// optionOffered reads the page for the option's fieldset: present and not
// hidden is offered; present and hidden, or absent, is not.
func (w *forkWorld) optionOffered(body, opt string, want bool) error {
	m := regexp.MustCompile(`<fieldset[^>]*\bid="opt-` + regexp.QuoteMeta(opt) + `"[^>]*>`).FindString(body)
	offered := m != "" && !strings.Contains(m, " hidden")
	if offered != want {
		return fmt.Errorf("option %q offered=%v (tag %q), want %v", opt, offered, m, want)
	}
	return nil
}

type forkPlanJSON struct {
	Plan struct {
		Days []struct {
			Day   int     `json:"day"`
			Miles float64 `json:"miles"`
			Long  bool    `json:"long_day"`
		} `json:"days"`
	} `json:"plan"`
}

func (w *forkWorld) planJSON(query string) (*forkPlanJSON, error) {
	target := "/plan.json"
	if query != "" {
		target += "?" + query
	}
	rec := send(w.t, w.app, "GET", target, testToken, "", nil)
	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("plan.json answered %d: %s", rec.Code, rec.Body.String())
	}
	var p forkPlanJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (w *forkWorld) planIs(query string, days, day int, long bool) error {
	p, err := w.planJSON(query)
	if err != nil {
		return err
	}
	if len(p.Plan.Days) != days {
		return fmt.Errorf("the plan has %d days, want %d", len(p.Plan.Days), days)
	}
	for _, d := range p.Plan.Days {
		if d.Day == day {
			if d.Long != long {
				return fmt.Errorf("day %d long=%v, want %v", day, d.Long, long)
			}
			return nil
		}
	}
	return fmt.Errorf("the plan has no day %d", day)
}

func (w *forkWorld) cp(name string) *Checkpoint {
	for _, l := range mustLegs(w.t, w.app, w.slug) {
		for i := range l.Checkpoints {
			if l.Checkpoints[i].Name == name {
				return &l.Checkpoints[i]
			}
		}
	}
	return nil
}

func (w *forkWorld) arrive(name string) error {
	c := w.cp(name)
	if c == nil {
		return fmt.Errorf("no stop named %q", name)
	}
	return w.app.store.SetArrived(c.ID, time.Now(), nil)
}

type forkHUD struct {
	Tonight    string  `json:"tonight_name"`
	Waze       string  `json:"waze"`
	MilesLeftN float64 `json:"miles_left_n"`
}

// hud asks the real /driver/hud.json with no position, as a phone with no fix
// would: the furthest arrival stands in for where the car is.
func (w *forkWorld) hud() (*forkHUD, error) {
	rec := send(w.t, w.app, "GET", "/driver/hud.json", testToken, "", nil)
	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("hud.json answered %d: %s", rec.Code, rec.Body.String())
	}
	var h forkHUD
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		return nil, err
	}
	return &h, nil
}
