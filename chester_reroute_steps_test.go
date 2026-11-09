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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/chester_reroute.feature. The Messages API is the apiStub
// (nothing reaches Anthropic). "Chester will ..." steps script one model turn
// each; the trip-keeper's question runs the real /chester/ask route and Save
// runs the real POST /driver/plan, as the card's button does.

type rerouteEvent struct {
	T        string          `json:"t"`
	D        string          `json:"d"`
	Proposal *rerouteCard    `json:"proposal"`
	Raw      json.RawMessage `json:"-"`
}

type rerouteCard struct {
	Option      string            `json:"option"`
	OptionTitle string            `json:"option_title"`
	Choice      string            `json:"choice"`
	ChoiceLabel string            `json:"choice_label"`
	Detail      string            `json:"detail"`
	MilesDelta  string            `json:"miles_delta"`
	HoursDelta  string            `json:"hours_delta"`
	InProgress  bool              `json:"in_progress"`
	Why         string            `json:"why"`
	Selection   map[string]string `json:"selection"`
	Start       string            `json:"start"`
	QueryID     int64             `json:"query_id"`
}

type rerouteWorld struct {
	t       *testing.T
	script  []http.HandlerFunc
	stub    *apiStub
	app     *App
	cookie  *http.Cookie
	noOpts  bool
	today   bool
	started bool
	evs     []rerouteEvent
	save    *httptest.ResponseRecorder
}

func initChesterRerouteScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &rerouteWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = rerouteWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^the trip has route choices$`, func() error { w.build(); return nil })
	sc.Step(`^the trip has no route choices$`, func() error { w.noOpts = true; w.build(); return nil })
	sc.Step(`^a post is already made on leg 1$`, func() error { w.build(); return w.postOnLeg1() })
	sc.Step(`^a post is made on leg 1 before the trip-keeper taps Save$`, func() error { return w.postOnLeg1() })
	sc.Step(`^day 1 has been driven to its last stop$`, w.day1Driven)
	sc.Step(`^the drive has started on day 1$`, w.driveStarted)
	sc.Step(`^Chester will propose switching "([^"]*)" to "([^"]*)" because "([^"]*)"$`, w.willPropose)
	sc.Step(`^Chester will (?:then )?say "([^"]*)"$`, w.willWrite)
	sc.Step(`^the trip-keeper asks Chester about the road "([^"]*)"$`, w.ask)
	sc.Step(`^Chester was told the route choices$`, func() error {
		if w.block() == "" {
			return fmt.Errorf("the plan block holds no ROUTE CHOICES")
		}
		return nil
	})
	sc.Step(`^Chester was told no route choices$`, func() error {
		if w.block() != "" {
			return fmt.Errorf("the plan block holds ROUTE CHOICES")
		}
		return nil
	})
	sc.Step(`^Chester was told "([^"]*)" would change the whole trip by "([^"]*)"$`, w.toldDelta)
	sc.Step(`^Chester was told "([^"]*)" is "([^"]*)"$`, w.toldState)
	sc.Step(`^Chester was told "([^"]*)"$`, func(s string) error { return w.toldInTail(s) })
	sc.Step(`^Chester was offered no propose tool$`, w.noProposeTool)
	sc.Step(`^the answer carries a proposal for "([^"]*)" to "([^"]*)"$`, w.hasProposal)
	sc.Step(`^the answer carries no proposal$`, w.noProposal)
	sc.Step(`^the proposal changes the trip by "([^"]*)" miles and "([^"]*)" hours$`, w.proposalDelta)
	sc.Step(`^the proposal says "([^"]*)"$`, w.proposalSays)
	sc.Step(`^the proposal card is marked in progress$`, w.proposalInProgress)
	sc.Step(`^the answer was written after the proposal$`, w.writtenAfter)
	sc.Step(`^the route answer says "([^"]*)"$`, w.answerSays)
	sc.Step(`^the log row's note says "([^"]*)"$`, w.noteSays)
	sc.Step(`^the log row records the proposal "([^"]*)" to "([^"]*)" as "([^"]*)"$`, w.logRecords)
	sc.Step(`^the page builds the proposal card with a Save button that posts to the plan save$`, w.pageBuilds)
	sc.Step(`^the trip-keeper taps Save on the proposal$`, w.tapSave)
	sc.Step(`^the trip-keeper taps Save on the proposal with the Chester id "([^"]*)"$`, w.tapSaveWithID)
	sc.Step(`^the log row's proposal outcome is "([^"]*)"$`, func(o string) error { return w.outcomeIs(o, false) })
	sc.Step(`^the log row's proposal outcome is "([^"]*)" with a reason$`, func(o string) error { return w.outcomeIs(o, true) })
	sc.Step(`^the page's Save sends the card's query id$`, w.pageSendsID)
	sc.Step(`^the plan save response carries only status, selection and plan$`, w.saveKeys)
	sc.Step(`^the plan save answers "([^"]*)"$`, func(s string) error { return w.saveAnswers(s, false) })
	sc.Step(`^the plan save answers "([^"]*)" with a reason$`, func(s string) error { return w.saveAnswers(s, true) })
	sc.Step(`^the saved plan takes "([^"]*)" for "([^"]*)"$`, w.savedTakes)
}

func (w *rerouteWorld) build() {
	if w.app != nil {
		return
	}
	w.stub = newAPIStub(w.t)
	if !w.noOpts && !w.today {
		w.app = chesterApp(w.t, w.stub)
	} else {
		app := tokenApp(w.t, testToken)
		var doc map[string]any
		if err := json.Unmarshal(fixtureTrip(w.t), &doc); err != nil {
			w.t.Fatal(err)
		}
		if w.today {
			ride := doc["ride"].(map[string]any)
			ride["start_date"] = time.Now().In(loadLoc("America/Chicago")).Format(dateLayout)
		}
		if w.noOpts {
			delete(doc, "options")
			var keep []any
			for _, f := range doc["features"].([]any) {
				pr := f.(map[string]any)["properties"].(map[string]any)
				if c, _ := pr["choice"].(string); c != "" && c != "ridge" && c != "direct" {
					continue
				}
				delete(pr, "option")
				delete(pr, "choice")
				keep = append(keep, f)
			}
			doc["features"] = keep
		}
		raw, _ := json.Marshal(doc)
		if r := pushTrip(w.t, app, raw, "?activate=1"); r.Code != http.StatusOK {
			w.t.Fatalf("push: %d %s", r.Code, r.Body.String())
		}
		app.chester = newChester(chesterEnv(w.t, w.stub, nil))
		w.app = app
	}
	w.cookie = asUser(w.t, w.app, "kris", roleAdmin)
}

func (w *rerouteWorld) postOnLeg1() error {
	legs, err := w.app.store.Legs(w.app.activeSlug())
	if err != nil || len(legs) == 0 {
		return fmt.Errorf("legs: %v", err)
	}
	return w.app.store.CreatePost(&Post{LegID: legs[0].ID, Kind: "note", OccurredAt: utcString(time.Now()),
		TZ: "America/Chicago", Title: "on the road"})
}

// day1Driven stamps day 1's last stop arrived: a road driven to its end is
// spent, posts or not, and that is what locks it.
func (w *rerouteWorld) day1Driven() error {
	legs, err := w.app.store.Legs(w.app.activeSlug())
	if err != nil || len(legs) == 0 || len(legs[0].Checkpoints) == 0 {
		return fmt.Errorf("legs: %v", err)
	}
	last := legs[0].Checkpoints[len(legs[0].Checkpoints)-1]
	return w.app.store.SetArrived(last.ID, time.Now(), nil)
}

func (w *rerouteWorld) driveStarted() error {
	w.today = true
	w.app = nil
	w.build()
	rv, err := w.app.loadRide()
	if err != nil {
		return err
	}
	return w.app.store.SetArrived(tripStart(rv.Legs).ID, time.Now(), nil)
}

func (w *rerouteWorld) willPropose(opt, choice, why string) error {
	in := js(map[string]any{"option": opt, "choice": choice, "why": why})
	w.script = append(w.script, stream(events(evStart(stubModel, 120, 0, 0), evThinking(0),
		evClientTool(1, "toolu_p"+strconv.Itoa(len(w.script)), "propose_route_choice", in), evEnd("tool_use", 20, 0, nil))...))
	return nil
}

func (w *rerouteWorld) willWrite(text string) error {
	w.script = append(w.script, stream(events(evStart(stubModel, 150, 0, 0), evText(0, text), evEnd("end_turn", 10, 0, nil))...))
	return nil
}

func (w *rerouteWorld) ask(q string) error {
	w.build()
	w.stub.mu.Lock()
	w.stub.script = w.script
	w.stub.mu.Unlock()
	rec := askForm(w.t, w.app, url.Values{"q": {q}})
	if rec.Code != http.StatusOK {
		return fmt.Errorf("ask: %d %s", rec.Code, rec.Body.String())
	}
	sc := bufio.NewScanner(bytes.NewReader(rec.Body.Bytes()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ev rerouteEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return fmt.Errorf("a line of the answer is not JSON: %q", sc.Text())
		}
		w.evs = append(w.evs, ev)
	}
	return nil
}

// noProposeTool reads the tool list the first request carried.
func (w *rerouteWorld) noProposeTool() error {
	var req struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(w.stub.requests()[0].Body, &req); err != nil || len(req.Tools) == 0 {
		return fmt.Errorf("the first request has no tool list (%v)", err)
	}
	for _, tl := range req.Tools {
		if tl.Name == "propose_route_choice" {
			return fmt.Errorf("the propose tool was offered")
		}
	}
	return nil
}

func (w *rerouteWorld) first() string { return string(w.stub.requests()[0].Body) }

func (w *rerouteWorld) firstHas(s string) error {
	if !strings.Contains(w.first(), s) {
		return fmt.Errorf("the first request does not hold %q", s)
	}
	return nil
}

func (w *rerouteWorld) firstLacks(s string) error {
	if strings.Contains(w.first(), s) {
		return fmt.Errorf("the first request holds %q", s)
	}
	return nil
}

// block is the ROUTE CHOICES block as the model was given it, JSON-unescaped.
func (w *rerouteWorld) block() string {
	var req struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	if err := json.Unmarshal(w.stub.requests()[0].Body, &req); err != nil {
		return ""
	}
	for _, s := range req.System {
		if i := strings.Index(s.Text, "\n\nROUTE CHOICES."); i >= 0 {
			return s.Text[i:]
		}
	}
	return ""
}

func (w *rerouteWorld) lineWith(id string) string {
	for _, l := range strings.Split(w.block(), "\n") {
		if strings.Contains(l, "["+id+"]") || strings.Contains(l, "id "+id) {
			return l
		}
	}
	return ""
}

func (w *rerouteWorld) toldDelta(choice, want string) error {
	l := w.lineWith(choice)
	if l == "" || !strings.Contains(l, want) {
		return fmt.Errorf("the line for %q is %q, want it to hold %q", choice, l, want)
	}
	return nil
}

func (w *rerouteWorld) toldState(id, state string) error {
	l := w.lineWith(id)
	if l == "" || !strings.Contains(l, state) {
		return fmt.Errorf("the line for %q is %q, want it to hold %q", id, l, state)
	}
	return nil
}

// toldInTail is what the model was told in a later request (a tool result).
func (w *rerouteWorld) toldInTail(s string) error {
	reqs := w.stub.requests()
	for _, r := range reqs[1:] {
		if strings.Contains(string(r.Body), s) {
			return nil
		}
	}
	return fmt.Errorf("no later request holds %q", s)
}

func (w *rerouteWorld) proposal() *rerouteCard {
	for _, e := range w.evs {
		if e.T == "proposal" && e.Proposal != nil {
			return e.Proposal
		}
	}
	return nil
}

func (w *rerouteWorld) hasProposal(opt, choice string) error {
	p := w.proposal()
	if p == nil {
		return fmt.Errorf("no proposal event in the answer")
	}
	if p.Option != opt || p.Choice != choice {
		return fmt.Errorf("the proposal is %s/%s, want %s/%s", p.Option, p.Choice, opt, choice)
	}
	return nil
}

func (w *rerouteWorld) noProposal() error {
	if w.proposal() != nil {
		return fmt.Errorf("the answer carries a proposal")
	}
	return nil
}

func (w *rerouteWorld) proposalDelta(miles, hours string) error {
	p := w.proposal()
	if p == nil {
		return fmt.Errorf("no proposal")
	}
	if p.MilesDelta != miles || p.HoursDelta != hours {
		return fmt.Errorf("the proposal changes the trip by %q miles, %q hours; want %q and %q", p.MilesDelta, p.HoursDelta, miles, hours)
	}
	return nil
}

func (w *rerouteWorld) proposalSays(s string) error {
	if p := w.proposal(); p == nil || p.Why != s {
		return fmt.Errorf("the proposal's why is not %q", s)
	}
	return nil
}

func (w *rerouteWorld) proposalInProgress() error {
	if p := w.proposal(); p == nil || !p.InProgress {
		return fmt.Errorf("the proposal is not marked in progress")
	}
	return nil
}

func (w *rerouteWorld) text() string {
	var b strings.Builder
	for _, e := range w.evs {
		if e.T == "text" {
			b.WriteString(e.D)
		}
	}
	return b.String()
}

func (w *rerouteWorld) writtenAfter() error {
	if w.proposal() == nil || strings.TrimSpace(w.text()) == "" {
		return fmt.Errorf("want a proposal and an answer")
	}
	return nil
}

func (w *rerouteWorld) answerSays(s string) error {
	if !strings.Contains(w.text(), s) {
		return fmt.Errorf("the answer is %q, want it to say %q", w.text(), s)
	}
	return nil
}

func (w *rerouteWorld) logJSON() (map[string]any, error) {
	// The row was asked with the push token, so it is unattributed: the
	// export reads it by token, as exportRow does.
	rec := send(w.t, w.app, "GET", "/driver/chester/log.json", testToken, "", nil)
	var any1 any
	if err := json.Unmarshal(rec.Body.Bytes(), &any1); err != nil {
		return nil, fmt.Errorf("log.json: %v: %s", err, rec.Body.String())
	}
	switch v := any1.(type) {
	case []any:
		if len(v) > 0 {
			return v[0].(map[string]any), nil
		}
	case map[string]any:
		for _, k := range []string{"queries"} {
			if rows, ok := v[k].([]any); ok && len(rows) > 0 {
				return rows[0].(map[string]any), nil
			}
		}
	}
	return nil, fmt.Errorf("no row in log.json: %s", rec.Body.String())
}

func (w *rerouteWorld) noteSays(s string) error {
	row, err := w.logJSON()
	if err != nil {
		return err
	}
	if n, _ := row["note"].(string); !strings.Contains(n, s) {
		return fmt.Errorf("the log note is %q, want it to say %q", n, s)
	}
	return nil
}

func (w *rerouteWorld) logRecords(opt, choice, outcome string) error {
	row, err := w.logJSON()
	if err != nil {
		return err
	}
	p, _ := row["proposal"].(map[string]any)
	if p == nil || p["option"] != opt || p["choice"] != choice || p["outcome"] != outcome {
		return fmt.Errorf("the log row's proposal is %v", row["proposal"])
	}
	return nil
}

func (w *rerouteWorld) pageBuilds() error {
	js, err := os.ReadFile("static/js/chester.js")
	if err != nil {
		return err
	}
	for _, want := range []string{"'proposal'", "'/driver/plan'", "'Save'", "plan estimate, not traffic", "opt-"} {
		if !strings.Contains(string(js), want) {
			return fmt.Errorf("static/js/chester.js does not build the proposal card: missing %q", want)
		}
	}
	html, err := os.ReadFile("templates/chester.html")
	if err != nil || !strings.Contains(string(html), `id="ch-proposal"`) {
		return fmt.Errorf("templates/chester.html has no ch-proposal list")
	}
	return nil
}

// tapSave posts what the card's Save button posts: every key of the card's
// selection as opt-<key>, and the start, asking for JSON.
func (w *rerouteWorld) tapSave() error {
	p := w.proposal()
	if p == nil {
		return fmt.Errorf("no proposal to save")
	}
	return w.postSave(p, fmt.Sprint(p.QueryID))
}

func (w *rerouteWorld) tapSaveWithID(id string) error {
	p := w.proposal()
	if p == nil {
		return fmt.Errorf("no proposal to save")
	}
	return w.postSave(p, id)
}

func (w *rerouteWorld) postSave(p *rerouteCard, chesterID string) error {
	form := url.Values{"start": {p.Start}, "chester_id": {chesterID}}
	for k, v := range p.Selection {
		form.Set("opt-"+k, v)
	}
	req := httptest.NewRequest("POST", "/driver/plan", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(w.cookie)
	w.save = httptest.NewRecorder()
	w.app.routes().ServeHTTP(w.save, req)
	return nil
}

func (w *rerouteWorld) saveAnswers(status string, reason bool) error {
	var j struct{ Status, Error string }
	if err := json.Unmarshal(w.save.Body.Bytes(), &j); err != nil {
		return fmt.Errorf("save: %d %s", w.save.Code, w.save.Body.String())
	}
	if j.Status != status {
		return fmt.Errorf("the plan save answered %q (%s), want %q", j.Status, w.save.Body.String(), status)
	}
	if reason && j.Error == "" {
		return fmt.Errorf("a refusal with no reason")
	}
	return nil
}

func (w *rerouteWorld) savedTakes(choice, opt string) error {
	rec := doAs(w.t, w.app, w.cookie, "GET", "/plan.json", nil)
	var j struct {
		Saved struct {
			Selection map[string]string `json:"selection"`
		} `json:"saved"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &j); err != nil {
		return err
	}
	if j.Saved.Selection[opt] != choice {
		return fmt.Errorf("the saved plan takes %q for %q, want %q (%s)", j.Saved.Selection[opt], opt, choice, rec.Body.String())
	}
	return nil
}

func (w *rerouteWorld) outcomeIs(outcome string, reason bool) error {
	row, err := w.logJSON()
	if err != nil {
		return err
	}
	p, _ := row["proposal"].(map[string]any)
	if p == nil || p["outcome"] != outcome {
		return fmt.Errorf("the log row's proposal is %v, want outcome %q", row["proposal"], outcome)
	}
	if r, _ := p["reason"].(string); reason && r == "" {
		return fmt.Errorf("the outcome %q carries no reason: %v", outcome, p)
	}
	return nil
}

func (w *rerouteWorld) pageSendsID() error {
	js, err := os.ReadFile("static/js/chester.js")
	if err != nil {
		return err
	}
	if !strings.Contains(string(js), "'chester_id'") {
		return fmt.Errorf("static/js/chester.js does not send chester_id with Save")
	}
	return nil
}

func (w *rerouteWorld) saveKeys() error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(w.save.Body.Bytes(), &m); err != nil {
		return err
	}
	for _, k := range []string{"status", "selection", "plan"} {
		if _, ok := m[k]; !ok {
			return fmt.Errorf("the save response lacks %q: %s", k, w.save.Body.String())
		}
	}
	if len(m) != 3 {
		return fmt.Errorf("the save response has extra keys: %s", w.save.Body.String())
	}
	return nil
}
