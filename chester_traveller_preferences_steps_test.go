package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/chester_traveller_preferences.feature. The Messages API
// is the apiStub: nothing here reaches Anthropic. The preferences are set
// through the real POST /chester/preferences route and read back through the
// real mux, as a phone or a machine would.

type prefWorld struct {
	t      *testing.T
	stub   *apiStub
	app    *App
	script []http.HandlerFunc
	last   *httptest.ResponseRecorder
	page   string
	button struct{ id, question string }
	before string
}

func initChesterPreferencesScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &prefWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = prefWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^the trip-keeper has saved Kris's preferences$`, w.savedKris)
	sc.Step(`^the trip-keeper saves every preference blank$`, w.savedBlank)
	sc.Step(`^the trip-keeper saves the places to avoid as "([^"]*)"$`, w.savesAvoid)
	sc.Step(`^the stubbed API will answer "([^"]*)"$`, w.willWrite)
	sc.Step(`^the trip-keeper puts "([^"]*)" to Chester$`, w.ask)
	sc.Step(`^the API was told a PREFERENCES block$`, func() error { _, err := w.block(); return err })
	sc.Step(`^the API was told no PREFERENCES block$`, w.noBlock)
	sc.Step(`^the PREFERENCES block says "([^"]*)"$`, w.blockSays)
	sc.Step(`^the API was told nothing of "([^"]*)" or "([^"]*)" or "([^"]*)" or "([^"]*)"$`, w.blockLacks)
	sc.Step(`^the prompt version on the row is "([^"]*)"$`, w.versionIs)
	sc.Step(`^the trip-keeper saves "([^"]*)" as "([^"]*)"$`, func(f, v string) error { return w.savesField(f, v, "", "") })
	sc.Step(`^the trip-keeper saves "([^"]*)" as "([^"]*)" with "([^"]*)" as "([^"]*)"$`, w.savesField)
	sc.Step(`^the save is refused naming "([^"]*)"$`, w.refusedNaming)
	sc.Step(`^nothing was saved$`, w.nothingSaved)
	sc.Step(`^the trip-keeper taps "([^"]*)" on the Chester page$`, w.taps)
	sc.Step(`^the API was asked the sentence on that button$`, w.askedButton)
	sc.Step(`^the sentence on that button says "([^"]*)"$`, w.buttonSays)
	sc.Step(`^the answer was logged as an ordinary preset question$`, w.loggedPreset)
	sc.Step(`^the trip-keeper opens the Chester page$`, w.opens)
	sc.Step(`^the form is labelled in plain words$`, w.labelled)
	sc.Step(`^the form shows the saved values$`, w.showsValues)
	sc.Step(`^the page keeps no preference in the browser and opens no dialog$`, w.noStorage)
	sc.Step(`^a signed-out visitor asks for the preferences and tries to save some$`, w.visitor)
	sc.Step(`^the visitor is sent to sign in or refused, and sees no preference$`, w.visitorRefused)
	sc.Step(`^the saved preferences are unchanged$`, w.unchanged)
	sc.Step(`^a machine posts Kris's preferences with the bearer token$`, w.machinePosts)
	sc.Step(`^the machine is told they are saved, with the values$`, w.machineTold)
	sc.Step(`^the server holds one record for the active ride$`, w.oneRecord)
	sc.Step(`^reading them back with the bearer token gives the same values$`, w.readBack)
	sc.Step(`^the system prompt defines Stop, Milestone and Waypoint$`, w.promptHas("a Stop is a place the travellers stay for the night", "a Milestone is a place they pass", "a Waypoint is a point that aids routing"))
	sc.Step(`^the system prompt asks for extra minutes on things to do and leaves out what is past the limit$`, w.promptHas("roughly how many extra minutes", "leave out anything", "past the traveller's limit"))
	sc.Step(`^the system prompt asks for towns before and after the Stop with lodging inside the price band, saying where each price was seen$`, w.promptHas("one or two towns a little before and after", "inside the traveller's price band", "where each price was seen"))
	sc.Step(`^the system prompt never states a price as certain$`, w.promptHas("never state a price as certain"))
	sc.Step(`^the system prompt says Chester only suggests and the plan changes by the trip-keeper's tap$`, w.promptHas("only suggest", "trip-keeper's tap"))
}

func (w *prefWorld) ensure() {
	if w.app == nil {
		w.stub = newAPIStub(w.t)
		w.app = chesterApp(w.t, w.stub)
	}
}

var krisPrefs = url.Values{"day_end_from": {"19:00"}, "day_end_to": {"21:00"}, "price_low": {"80"}, "price_high": {"120"},
	"avoid": {"the south shores of Lake Ontario and Lake Erie"}, "navigator": {"Waze"}, "max_extra_min": {"20"}}

func (w *prefWorld) save(form url.Values) *httptest.ResponseRecorder {
	w.ensure()
	return postForm(w.t, w.app, "/chester/preferences", form)
}

func (w *prefWorld) savedKris() error {
	if rec := w.save(krisPrefs); rec.Code != http.StatusOK {
		return fmt.Errorf("saving Kris's preferences: %d %s", rec.Code, rec.Body.String())
	}
	return nil
}

func (w *prefWorld) savedBlank() error {
	blank := url.Values{}
	for k := range krisPrefs {
		blank.Set(k, "")
	}
	if rec := w.save(blank); rec.Code != http.StatusOK {
		return fmt.Errorf("saving blank preferences: %d %s", rec.Code, rec.Body.String())
	}
	return nil
}

func (w *prefWorld) savesAvoid(text string) error {
	f := url.Values{"avoid": {text}}
	if rec := w.save(f); rec.Code != http.StatusOK {
		return fmt.Errorf("saving the places to avoid: %d %s", rec.Code, rec.Body.String())
	}
	return nil
}

func (w *prefWorld) willWrite(text string) error {
	w.script = append(w.script, stream(events(evStart(stubModel, 150, 0, 0), evText(0, text), evEnd("end_turn", 10, 0, nil))...))
	return nil
}

func (w *prefWorld) ask(q string) error {
	w.ensure()
	w.stub.mu.Lock()
	w.stub.script = append([]http.HandlerFunc(nil), w.script...)
	w.stub.mu.Unlock()
	rec := askForm(w.t, w.app, url.Values{"q": {q}})
	if rec.Code != http.StatusOK {
		return fmt.Errorf("ask: %d %s", rec.Code, rec.Body.String())
	}
	return nil
}

// strings collects every string in the first request, so a block is read the
// way the model reads it and not through JSON escaping.
func requestStrings(v any, out *[]string) {
	switch x := v.(type) {
	case string:
		*out = append(*out, x)
	case []any:
		for _, e := range x {
			requestStrings(e, out)
		}
	case map[string]any:
		for _, e := range x {
			requestStrings(e, out)
		}
	}
}

// block returns the PREFERENCES block as the API was told it.
func (w *prefWorld) block() (string, error) {
	if w.stub == nil || len(w.stub.requests()) == 0 {
		return "", fmt.Errorf("the API was never asked")
	}
	var all []string
	requestStrings(w.stub.request(0), &all)
	for _, s := range all {
		i := strings.Index(s, "PREFERENCES:\n")
		if i < 0 {
			continue
		}
		var b []string
		for _, ln := range strings.Split(s[i:], "\n") {
			if len(b) > 0 && !strings.HasPrefix(ln, "- ") {
				break
			}
			b = append(b, ln)
		}
		return strings.Join(b, "\n"), nil
	}
	return "", fmt.Errorf("the API was told no PREFERENCES block")
}

func (w *prefWorld) noBlock() error {
	if b, err := w.block(); err == nil {
		return fmt.Errorf("the API was told a block it should not have been: %s", b)
	}
	if body := string(w.stub.requests()[0].Body); strings.Contains(body, "PREFERENCES:") {
		return fmt.Errorf("the request still carries PREFERENCES:")
	}
	return nil
}

func (w *prefWorld) blockSays(s string) error {
	b, err := w.block()
	if err != nil {
		return err
	}
	if !strings.Contains(b, s) {
		return fmt.Errorf("the block does not say %q:\n%s", s, b)
	}
	return nil
}

func (w *prefWorld) blockLacks(a, b, c, d string) error {
	blk, err := w.block()
	if err != nil {
		return err
	}
	for _, s := range []string{a, b, c, d} {
		if strings.Contains(blk, s) {
			return fmt.Errorf("the block holds %q:\n%s", s, blk)
		}
	}
	return nil
}

func (w *prefWorld) versionIs(v string) error {
	rows := chesterRows(w.t, w.app)
	if len(rows) != 1 || rows[0].PromptVersion != v || chesterPromptVersion != v {
		return fmt.Errorf("prompt version: constant %q, rows %+v, want %q", chesterPromptVersion, rows, v)
	}
	return nil
}

func (w *prefWorld) snapshot() string {
	return send(w.t, w.app, "GET", "/chester/preferences", testToken, "", nil).Body.String()
}

func (w *prefWorld) savesField(f, v, f2, v2 string) error {
	w.ensure()
	w.before = w.snapshot()
	long := strings.Repeat("a", 300)
	form := url.Values{}
	for _, kv := range [][2]string{{f, v}, {f2, v2}} {
		if kv[0] == "" {
			continue
		}
		val := kv[1]
		if val == "AVOIDLONG" || val == "NAVLONG" {
			val = long
		}
		form.Set(kv[0], val)
	}
	w.last = w.save(form)
	return nil
}

func (w *prefWorld) refusedNaming(field string) error {
	if w.last.Code != http.StatusBadRequest {
		return fmt.Errorf("the save was answered %d, want 400: %s", w.last.Code, w.last.Body.String())
	}
	var out struct{ Status, Error string }
	if err := json.Unmarshal(w.last.Body.Bytes(), &out); err != nil {
		return fmt.Errorf("the refusal is not JSON: %s", w.last.Body.String())
	}
	if out.Status != "refused" || !strings.HasPrefix(out.Error, field+":") {
		return fmt.Errorf("the refusal %+v does not name %q first", out, field)
	}
	return nil
}

func (w *prefWorld) nothingSaved() error {
	if got := w.snapshot(); got != w.before {
		return fmt.Errorf("a refused save changed the record: %s then %s", w.before, got)
	}
	return nil
}

var presetButton = regexp.MustCompile(`data-preset="([^"]*)" data-question="([^"]*)">([^<]*)</button>`)

func (w *prefWorld) taps(label string) error {
	w.ensure()
	page := send(w.t, w.app, "GET", "/chester", testToken, "", nil).Body.String()
	for _, m := range presetButton.FindAllStringSubmatch(page, -1) {
		if strings.TrimSpace(m[3]) == label {
			w.button.id, w.button.question = m[1], htmlUnescape(m[2])
			w.stub.mu.Lock()
			w.stub.script = append(w.stub.script, w.script...)
			w.script = nil
			w.stub.mu.Unlock()
			rec := askForm(w.t, w.app, url.Values{"preset": {m[1]}})
			if rec.Code != http.StatusOK {
				return fmt.Errorf("tapping %q: %d %s", label, rec.Code, rec.Body.String())
			}
			return nil
		}
	}
	return fmt.Errorf("the Chester page has no button %q", label)
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&#39;", "'", "&amp;", "&", "&#34;", `"`, "&lt;", "<", "&gt;", ">").Replace(s)
}

func (w *prefWorld) askedButton() error {
	reqs := w.stub.requests()
	if len(reqs) == 0 || w.button.question == "" {
		return fmt.Errorf("nothing was asked")
	}
	var all []string
	requestStrings(w.stub.request(len(reqs)-1), &all)
	for _, s := range all {
		if strings.Contains(s, w.button.question) {
			return nil
		}
	}
	return fmt.Errorf("the API was not asked %q", w.button.question)
}

func (w *prefWorld) buttonSays(s string) error {
	if !strings.Contains(w.button.question, s) {
		return fmt.Errorf("the sentence %q does not say %q", w.button.question, s)
	}
	return nil
}

func (w *prefWorld) loggedPreset() error {
	rows := chesterRows(w.t, w.app)
	if len(rows) == 0 || rows[0].Preset != w.button.id || rows[0].Question != w.button.question {
		return fmt.Errorf("the newest row is not the preset %s: %+v", w.button.id, rows)
	}
	return nil
}

func (w *prefWorld) opens() error {
	w.ensure()
	rec := send(w.t, w.app, "GET", "/chester", testToken, "", nil)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("the page: %d", rec.Code)
	}
	w.page = rec.Body.String()
	return nil
}

func (w *prefWorld) labelled() error {
	for _, want := range []string{"Stop driving from", "Stop driving by", "Hotel price, lowest a night", "Hotel price, highest a night",
		"Places to avoid", "Who navigates", "Most extra minutes a thing on the way may add"} {
		if !strings.Contains(w.page, want) {
			return fmt.Errorf("the form has no label %q", want)
		}
	}
	if !strings.Contains(w.page, `action="/chester/preferences"`) {
		return fmt.Errorf("the form does not post to /chester/preferences")
	}
	return nil
}

func (w *prefWorld) showsValues() error {
	for name, val := range map[string]string{"day_end_from": "19:00", "day_end_to": "21:00", "price_low": "80", "price_high": "120",
		"navigator": "Waze", "max_extra_min": "20", "avoid": "the south shores of Lake Ontario and Lake Erie"} {
		if !regexp.MustCompile(`name="` + name + `"[^>]*value="` + regexp.QuoteMeta(val) + `"`).MatchString(w.page) {
			return fmt.Errorf("the form does not show %s = %q", name, val)
		}
	}
	return nil
}

func (w *prefWorld) noStorage() error {
	script, _ := staticFS.ReadFile("static/js/chester.js")
	// The read-aloud switch is a per-viewer convenience that was already there;
	// what must never reach browser storage is any preference.
	for _, bad := range []string{"sessionStorage", "indexedDB", "alert(", "confirm(", "prompt(", "showModal", "<dialog"} {
		if strings.Contains(w.page, bad) || strings.Contains(string(script), bad) {
			return fmt.Errorf("the page or its script holds %q", bad)
		}
	}
	for _, key := range []string{"day_end", "price_low", "price_high", "max_extra", "preferences", "avoid"} {
		if strings.Contains(string(script), key) {
			return fmt.Errorf("the page's script handles %q: preferences live on the server", key)
		}
	}
	return nil
}

func (w *prefWorld) visitor() error {
	w.ensure()
	w.before = w.snapshot()
	w.last = send(w.t, w.app, "GET", "/chester/preferences", "", "", nil)
	w.page = send(w.t, w.app, "GET", "/chester", "", "", nil).Body.String()
	save := send(w.t, w.app, "POST", "/chester/preferences", "", "application/x-www-form-urlencoded",
		[]byte(url.Values{"price_low": {"10"}, "price_high": {"20"}}.Encode()))
	if save.Code != http.StatusUnauthorized {
		return fmt.Errorf("a signed-out save was answered %d, want 401", save.Code)
	}
	return nil
}

func (w *prefWorld) visitorRefused() error {
	if w.last.Code != http.StatusFound && w.last.Code != http.StatusUnauthorized {
		return fmt.Errorf("a signed-out read was answered %d", w.last.Code)
	}
	for _, leak := range []string{"Waze", "Lake Ontario", "80", "Places to avoid"} {
		if strings.Contains(w.last.Body.String(), leak) || strings.Contains(w.page, leak) {
			return fmt.Errorf("a signed-out response holds %q", leak)
		}
	}
	return nil
}

func (w *prefWorld) unchanged() error {
	if got := w.snapshot(); got != w.before {
		return fmt.Errorf("the record changed: %s then %s", w.before, got)
	}
	return nil
}

func (w *prefWorld) machinePosts() error {
	w.last = w.save(krisPrefs)
	return nil
}

func (w *prefWorld) machineTold() error {
	if w.last.Code != http.StatusOK {
		return fmt.Errorf("answered %d: %s", w.last.Code, w.last.Body.String())
	}
	var out struct {
		Status      string
		Preferences struct {
			DayEndFrom string `json:"day_end_from"`
			PriceHigh  int    `json:"price_high"`
			Navigator  string `json:"navigator"`
		}
	}
	if err := json.Unmarshal(w.last.Body.Bytes(), &out); err != nil || out.Status != "saved" ||
		out.Preferences.DayEndFrom != "19:00" || out.Preferences.PriceHigh != 120 || out.Preferences.Navigator != "Waze" {
		return fmt.Errorf("the reply is not the saved values: %s", w.last.Body.String())
	}
	return nil
}

func (w *prefWorld) oneRecord() error {
	v, ok := w.app.store.Meta(metaPrefsKey(w.app.activeSlug()))
	if !ok || !strings.Contains(v, "Lake Ontario") {
		return fmt.Errorf("no record for the active ride %q in the server's store", w.app.activeSlug())
	}
	return nil
}

func (w *prefWorld) readBack() error {
	rec := send(w.t, w.app, "GET", "/chester/preferences", testToken, "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"price_high": 120`) || !strings.Contains(rec.Body.String(), "Lake Erie") {
		return fmt.Errorf("reading back: %d %s", rec.Code, rec.Body.String())
	}
	return nil
}

func (w *prefWorld) promptHas(wants ...string) func() error {
	return func() error {
		sys := js(w.stub.request(0)["system"])
		sys = strings.ReplaceAll(sys, `\u0027`, "'")
		for _, want := range wants {
			if !strings.Contains(sys, want) {
				return fmt.Errorf("the system prompt does not say %q", want)
			}
		}
		return nil
	}
}
