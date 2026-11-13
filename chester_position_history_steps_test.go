package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/chester_position_by_default_and_earlier_answers.feature.
// The Messages API is the apiStub; seeded asks are inserted straight into the
// store. The browser side (geolocation) is not driven here: only what the
// server renders and stores, plus a node check of positionPlan.

type posWorld struct {
	t       *testing.T
	app     *App
	cookies map[string]*http.Cookie
	page    string
	last    *httptest.ResponseRecorder
}

func initChesterPositionHistoryScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &posWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = posWorld{t: t, cookies: map[string]*http.Cookie{}}
		return ctx, nil
	})
	sc.Step(`^Kris is signed in and Chester is on$`, w.signedIn)
	sc.Step(`^Kris looks at the Chester page$`, func() error { return w.open("kris") })
	sc.Step(`^Dominic looks at the Chester page$`, func() error { return w.open("dominic") })
	sc.Step(`^the position row comes before "([^"]*)" and before "([^"]*)"$`, w.rowFirst)
	sc.Step(`^the page says "([^"]*)"$`, w.pageSays)
	sc.Step(`^the position button offers "([^"]*)" and can be turned back to "([^"]*)"$`, w.buttonLabels)
	sc.Step(`^the script keeps the position in no browser storage$`, w.noStorage)
	sc.Step(`^Kris asks "([^"]*)" with the position ([-0-9.]+) and ([-0-9.]+)$`, w.asksWith)
	sc.Step(`^Kris asks "([^"]*)" without a position$`, w.asksWithout)
	sc.Step(`^the row was logged with the source "([^"]*)"$`, w.source)
	sc.Step(`^no page shows the raw point ([-0-9.]+)$`, w.noRaw)
	sc.Step(`^the ask was answered and logged with no phone position$`, w.noPhonePos)
	sc.Step(`^Kris asked "([^"]*)" and was told "([^"]*)" with a card for "([^"]*)"$`, w.seedCard)
	sc.Step(`^Kris asked "([^"]*)" and Chester proposed "([^"]*)"$`, w.seedProposal)
	sc.Step(`^the Earlier answers list shows "([^"]*)" with "([^"]*)" and a card for "([^"]*)"$`, w.listShows)
	sc.Step(`^the Earlier answers list has nothing of Kris's$`, w.nothingOfKris)
	sc.Step(`^a signed-out visitor asks for the Chester page$`, w.visitor)
	sc.Step(`^the visitor is sent to sign in and sees no earlier answer$`, w.visitorRefused)
	sc.Step(`^the Earlier answers list shows the proposal words "([^"]*)" and no Save button$`, w.proposalNoSave)
}

func (w *posWorld) signedIn() error {
	stub := newAPIStub(w.t, quickScript(), quickScript())
	w.app = chesterApp(w.t, stub)
	w.cookies["kris"] = asUser(w.t, w.app, "kris", roleAdmin)
	w.cookies["dominic"] = asUser(w.t, w.app, "dominic", roleAdmin)
	return nil
}

func (w *posWorld) open(who string) error {
	w.last = doAs(w.t, w.app, w.cookies[who], "GET", "/chester", nil)
	if w.last.Code != http.StatusOK {
		return fmt.Errorf("/chester answered %d", w.last.Code)
	}
	w.page = w.last.Body.String()
	return nil
}

func (w *posWorld) rowFirst(a, b string) error {
	row := strings.Index(w.page, `id="ch-locate"`)
	if row < 0 {
		return fmt.Errorf("no position button on the page")
	}
	for _, s := range []string{a, b} {
		i := strings.Index(w.page, s)
		if i < 0 || row > i {
			return fmt.Errorf("the position row (at %d) is not before %q (at %d)", row, s, i)
		}
	}
	return nil
}

func (w *posWorld) pageSays(s string) error {
	if !strings.Contains(w.page, s) {
		return fmt.Errorf("the page does not say %q", s)
	}
	return nil
}

func (w *posWorld) buttonLabels(off, on string) error {
	for _, s := range []string{`data-off-label="` + off + `"`, `data-on-label="` + on + `"`, `aria-pressed="true"`} {
		if !strings.Contains(w.page, s) {
			return fmt.Errorf("the position button lacks %s", s)
		}
	}
	return nil
}

func (w *posWorld) noStorage() error {
	b, err := os.ReadFile("static/js/chester.js")
	if err != nil {
		return err
	}
	src := string(b)
	// The read-aloud choice may use localStorage; the position must not.
	for _, bad := range []string{"sessionStorage", "chester-pos", "indexedDB"} {
		if strings.Contains(src, bad) {
			return fmt.Errorf("chester.js uses %s", bad)
		}
	}
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "localStorage") && !strings.Contains(line, "chester-speak") {
			return fmt.Errorf("chester.js keeps something else in localStorage: %s", line)
		}
	}
	if _, err := exec.LookPath("node"); err != nil {
		w.t.Log("node is not installed here: positionPlan was not run")
		return nil
	}
	out, err := exec.Command("node", "-e", `const m=require('./static/js/chester.js');
const f={at:0};console.log([m.positionPlan(true,false,true,null,0),m.positionPlan(true,false,true,f,30000),m.positionPlan(true,false,true,f,90000),m.positionPlan(false,false,true,f,0),m.positionPlan(true,true,true,null,0),m.positionPlan(true,false,false,null,0)].join())`).CombinedOutput()
	if err != nil {
		return fmt.Errorf("node: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "ask,use,ask,none,none,none" {
		return fmt.Errorf("positionPlan says %s", got)
	}
	return nil
}

func (w *posWorld) ask(q string, extra url.Values) {
	form := url.Values{"q": {q}}
	for k, v := range extra {
		form[k] = v
	}
	w.last = doAs(w.t, w.app, w.cookies["kris"], "POST", "/chester/ask", form)
	if w.last.Code != http.StatusOK {
		w.t.Fatalf("the ask answered %d: %s", w.last.Code, w.last.Body.String())
	}
}

func (w *posWorld) asksWith(q, lat, lng string) error {
	w.ask(q, url.Values{"lat": {lat}, "lng": {lng}})
	return nil
}

func (w *posWorld) asksWithout(q string) error {
	w.ask(q, nil)
	return nil
}

func (w *posWorld) latest() (ChesterQuery, error) {
	rows := chesterRows(w.t, w.app)
	if len(rows) == 0 {
		return ChesterQuery{}, fmt.Errorf("no row was logged")
	}
	return rows[0], nil
}

func (w *posWorld) source(want string) error {
	r, err := w.latest()
	if err != nil {
		return err
	}
	if r.PosSource != want || r.PosLat == nil {
		return fmt.Errorf("the row's position source is %q (lat set: %v), want %q", r.PosSource, r.PosLat != nil, want)
	}
	return nil
}

func (w *posWorld) noRaw(raw string) error {
	if strings.Contains(w.last.Body.String(), raw) {
		return fmt.Errorf("the ask's answer carries the raw point %s", raw)
	}
	if err := w.open("kris"); err != nil {
		return err
	}
	if strings.Contains(w.page, raw) {
		return fmt.Errorf("the page carries the raw point %s", raw)
	}
	return nil
}

func (w *posWorld) noPhonePos() error {
	r, err := w.latest()
	if err != nil {
		return err
	}
	if r.PosSource == "browser" {
		return fmt.Errorf("the row says the phone sent a position")
	}
	if r.Answer == "" && r.Error != "" {
		return fmt.Errorf("the ask failed: %s", r.Error)
	}
	return nil
}

func (w *posWorld) seed(q ChesterQuery) {
	q.Ride, q.TZ, q.Mode, q.Model = w.app.activeSlug(), "UTC", "quick", stubModel
	if q.AskedAt == "" {
		q.AskedAt = utcString(time.Now())
	}
	if err := w.app.store.InsertChesterQuery(&q); err != nil {
		w.t.Fatal(err)
	}
}

func (w *posWorld) seedCard(q, ans, place string) error {
	w.seed(ChesterQuery{User: "kris", Question: q, Answer: ans,
		Places: []chesterPlace{{Name: place, Kind: "fuel", Town: "Birchton", Why: "open late"}},
		Music:  []chesterMusicCard{{AlbumID: 7, Album: "Blue Road", Artist: "Someone", Label: "Play", Href: "https://example.test/a/7"}}})
	return nil
}

func (w *posWorld) seedProposal(q, words string) error {
	pr, _ := json.Marshal(map[string]string{"option": "route", "choice": words, "why": "fewer miles"})
	w.seed(ChesterQuery{User: "kris", Question: q, Answer: "Here is a change.", Proposal: pr})
	return nil
}

func (w *posWorld) listShows(q, ans, place string) error {
	for _, s := range []string{`class="ch-earlier"`, `<details class="ch-earlier-item">`, q, ans, place, "Blue Road", "/driver/chester/log"} {
		if !strings.Contains(w.page, s) {
			return fmt.Errorf("the page lacks %q", s)
		}
	}
	if strings.Index(w.page, `class="ch-earlier"`) > strings.Index(w.page, `class="ch-prefs card"`) ||
		strings.Index(w.page, `id="ch-answer"`) > strings.Index(w.page, `class="ch-earlier"`) {
		return fmt.Errorf("Earlier answers is not between the result area and the preferences card")
	}
	return nil
}

func (w *posWorld) nothingOfKris() error {
	for _, s := range []string{"Where is fuel?", "Birch Gate", "Blue Road"} {
		if strings.Contains(w.page, s) {
			return fmt.Errorf("another driver's page carries %q", s)
		}
	}
	return nil
}

func (w *posWorld) visitor() error {
	w.last = doAs(w.t, w.app, nil, "GET", "/chester", nil)
	return nil
}

func (w *posWorld) visitorRefused() error {
	if w.last.Code != http.StatusFound && w.last.Code != http.StatusUnauthorized {
		return fmt.Errorf("a signed-out request was answered %d", w.last.Code)
	}
	for _, s := range []string{"Where is fuel?", "Birch Gate", "Earlier answers"} {
		if strings.Contains(w.last.Body.String(), s) {
			return fmt.Errorf("a signed-out response carries %q", s)
		}
	}
	return nil
}

func (w *posWorld) proposalNoSave(words string) error {
	if !strings.Contains(w.page, words) {
		return fmt.Errorf("the proposal's words are not shown")
	}
	i := strings.Index(w.page, `class="ch-earlier"`)
	j := strings.Index(w.page, `class="ch-prefs card"`)
	if strings.Contains(w.page[i:j], ">Save<") || strings.Contains(w.page[i:j], "proposal_save") {
		return fmt.Errorf("the earlier list offers a Save")
	}
	return nil
}
