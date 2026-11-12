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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/chester_podcast.feature. The Messages API is the apiStub
// and Apple's search is a local httptest server: nothing here reaches
// Anthropic or Apple. The events are read back as the phone would read them.

type podcastEvent struct {
	T        string               `json:"t"`
	D        string               `json:"d"`
	Podcasts []chesterPodcastCard `json:"podcasts"`
}

type podcastWorld struct {
	t       *testing.T
	eps     []map[string]any
	down    bool
	script  []http.HandlerFunc
	stub    *apiStub
	app     *App
	fake    *fakeRedis
	srv     *httptest.Server
	mu      sync.Mutex
	queries []string
	evs     []podcastEvent
	raw     string
	page    string
	listen  bool
}

func initChesterPodcastScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &podcastWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		if w.srv != nil {
			w.srv.Close()
		}
		*w = podcastWorld{t: t}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w.srv != nil {
			w.srv.Close()
			w.srv = nil
		}
		return ctx, nil
	})
	sc.Step(`^the podcast search holds the episode "([^"]*)" "([^"]*)" of (\d+) minutes at "([^"]*)"$`, w.holds)
	sc.Step(`^the podcast search also holds the episode "([^"]*)" "([^"]*)" of (\d+) minutes at "([^"]*)"$`, w.holds)
	sc.Step(`^the podcast search is down$`, func() error { w.down = true; return nil })
	sc.Step(`^the bus is listening$`, func() error { w.listen = true; return nil })
	sc.Step(`^Chester will search podcasts for "([^"]*)"$`, w.willSearch)
	sc.Step(`^Chester will then show the podcast "([^"]*)" because "([^"]*)"$`, w.willShow)
	sc.Step(`^Chester will (?:then )?answer "([^"]*)"$`, w.willSay)
	sc.Step(`^the traveller asks Chester for "([^"]*)"$`, w.ask)
	sc.Step(`^the traveller opens the Chester page$`, w.openPage)
	sc.Step(`^the page has a preset button "([^"]*)"$`, w.pageHasPreset)
	sc.Step(`^the traveller taps the preset "([^"]*)"$`, w.tapPreset)
	sc.Step(`^the API was asked about the country the car is driving through, in the next two hours$`, w.presetReached)
	sc.Step(`^Chester was offered the tools "([^"]*)" and "([^"]*)"$`, w.offered)
	sc.Step(`^Chester was told about "([^"]*)"$`, func(s string) error { return w.told(s, true) })
	sc.Step(`^Chester was not told about "([^"]*)"$`, func(s string) error { return w.told(s, false) })
	sc.Step(`^the answer carries one podcast card$`, w.oneCard)
	sc.Step(`^the card names the show "([^"]*)" and the episode "([^"]*)"$`, w.cardNames)
	sc.Step(`^the card says (\d+) minutes and "([^"]*)"$`, w.cardSays)
	sc.Step(`^the card's button is "([^"]*)" and opens "([^"]*)"$`, w.cardButton)
	sc.Step(`^the podcast answer was written after the last tool call$`, w.writtenLast)
	sc.Step(`^the page builds the button as a new-tab link$`, w.pageBuilds)
	sc.Step(`^the search service was asked for podcast episodes, limit 25, with the words "([^"]*)"$`, w.askedWith)
	sc.Step(`^no digit follows a point in what the search service was sent$`, w.noCoordinate)
	sc.Step(`^Chester was told the podcast search is unavailable$`, w.toldDown)
	sc.Step(`^the answer says the podcast search is unavailable$`, w.answerSaysDown)
	sc.Step(`^the answer carries no podcast card$`, func() error {
		if cs := w.cards(); len(cs) != 0 {
			return fmt.Errorf("want no podcast card, got %+v", cs)
		}
		return nil
	})
	sc.Step(`^the logged row holds the podcast card for "([^"]*)"$`, w.loggedRow)
	sc.Step(`^the bus carries a chester_podcast_search record with the count (\d+) and not the query$`, w.bus)
}

func (w *podcastWorld) holds(show, ep, mins, link string) error {
	m, _ := strconv.Atoi(mins)
	w.eps = append(w.eps, map[string]any{"wrapperType": "podcastEpisode", "collectionName": show, "trackName": ep,
		"releaseDate": "2019-03-04T08:00:00Z", "trackTimeMillis": m * 60000, "trackViewUrl": link, "artistName": "Somebody"})
	return nil
}

func (w *podcastWorld) willSearch(q string) error {
	in := js(map[string]any{"query": q})
	w.script = append(w.script, stream(events(evStart(stubModel, 100, 0, 0), evThinking(0),
		evClientTool(1, "toolu_p"+strconv.Itoa(len(w.script)), "search_podcasts", in), evEnd("tool_use", 20, 0, nil))...))
	return nil
}

func (w *podcastWorld) willShow(ref, why string) error {
	in := js(map[string]any{"ref": ref, "why": why})
	w.script = append(w.script, stream(events(evStart(stubModel, 120, 0, 0), evThinking(0),
		evClientTool(1, "toolu_q"+strconv.Itoa(len(w.script)), "show_podcast", in), evEnd("tool_use", 20, 0, nil))...))
	return nil
}

func (w *podcastWorld) willSay(text string) error {
	w.script = append(w.script, stream(events(evStart(stubModel, 150, 0, 0), evText(0, text), evEnd("end_turn", 10, 0, nil))...))
	return nil
}

// build makes the app and the stubbed search once per scenario.
func (w *podcastWorld) build() {
	if w.app != nil {
		return
	}
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		w.queries = append(w.queries, r.URL.RawQuery)
		w.mu.Unlock()
		if w.down {
			http.Error(rw, "down", http.StatusServiceUnavailable)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(map[string]any{"resultCount": len(w.eps), "results": w.eps})
	}))
	w.stub = newAPIStub(w.t, w.script...)
	w.app = chesterApp(w.t, w.stub)
	w.app.podcasts = &podcastSvc{base: w.srv.URL, client: w.srv.Client()}
	if w.listen {
		w.fake = newFakeRedis(w.t, true)
		w.app.melt = newMelt(w.fake.addr(), "kate")
	}
}

func (w *podcastWorld) run(form url.Values) error {
	w.build()
	rec := askForm(w.t, w.app, form)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("ask: %d %s", rec.Code, rec.Body.String())
	}
	w.raw = rec.Body.String()
	sc := bufio.NewScanner(bytes.NewReader(rec.Body.Bytes()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ev podcastEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return fmt.Errorf("a line of the answer is not JSON: %q", sc.Text())
		}
		w.evs = append(w.evs, ev)
	}
	return nil
}

func (w *podcastWorld) ask(q string) error { return w.run(url.Values{"q": {q}}) }

func (w *podcastWorld) openPage() error {
	w.build()
	rec := send(w.t, w.app, "GET", "/chester", testToken, "", nil)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("/chester answered %d", rec.Code)
	}
	w.page = rec.Body.String()
	return nil
}

func (w *podcastWorld) pageHasPreset(label string) error {
	if !strings.Contains(w.page, ">"+label+"<") {
		return fmt.Errorf("the Chester page has no preset button %q", label)
	}
	return nil
}

func (w *podcastWorld) tapPreset(label string) error {
	for _, p := range chesterPresets {
		if p.Label == label {
			w.script = append(w.script, stream(events(evStart(stubModel, 150, 0, 0), evText(0, "Here."), evEnd("end_turn", 10, 0, nil))...))
			w.app = nil
			return w.run(url.Values{"preset": {p.ID}})
		}
	}
	return fmt.Errorf("no preset is called %q", label)
}

func (w *podcastWorld) presetReached() error {
	b := string(w.stub.requests()[0].Body)
	if !strings.Contains(b, "country we are driving through now") || !strings.Contains(b, "next two hours") {
		return fmt.Errorf("the preset question did not reach the API: %s", b)
	}
	return nil
}

func (w *podcastWorld) offered(a, b string) error {
	var req struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(w.stub.requests()[0].Body, &req)
	have := map[string]bool{}
	for _, t := range req.Tools {
		have[t.Name] = true
	}
	if !have[a] || !have[b] {
		return fmt.Errorf("tools offered: %v, want %s and %s", have, a, b)
	}
	return nil
}

func (w *podcastWorld) toolResults() string {
	var all strings.Builder
	for _, r := range w.stub.requests()[1:] {
		var req struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(r.Body, &req)
		for _, m := range req.Messages {
			if bytes.Contains(m.Content, []byte("tool_result")) {
				all.Write(m.Content)
			}
		}
	}
	return all.String()
}

func (w *podcastWorld) told(s string, want bool) error {
	if got := strings.Contains(w.toolResults(), s); got != want {
		return fmt.Errorf("the search result sent to the model holding %q = %v, want %v: %s", s, got, want, w.toolResults())
	}
	return nil
}

func (w *podcastWorld) cards() []chesterPodcastCard {
	var out []chesterPodcastCard
	for _, e := range w.evs {
		if e.T == "podcasts" {
			out = append(out, e.Podcasts...)
		}
	}
	return out
}

func (w *podcastWorld) text() string {
	var b strings.Builder
	for _, e := range w.evs {
		if e.T == "text" {
			b.WriteString(e.D)
		}
	}
	return b.String()
}

func (w *podcastWorld) one() (chesterPodcastCard, error) {
	cs := w.cards()
	if len(cs) != 1 {
		return chesterPodcastCard{}, fmt.Errorf("want one podcast card, got %+v (answer: %s)", cs, w.raw)
	}
	return cs[0], nil
}

func (w *podcastWorld) oneCard() error { _, err := w.one(); return err }

func (w *podcastWorld) cardNames(show, ep string) error {
	c, err := w.one()
	if err != nil {
		return err
	}
	if c.Show != show || c.Episode != ep {
		return fmt.Errorf("the card names %q / %q", c.Show, c.Episode)
	}
	return nil
}

func (w *podcastWorld) cardSays(mins int, why string) error {
	c, err := w.one()
	if err != nil {
		return err
	}
	if c.Minutes != mins || c.Why != why || c.Year != 2019 {
		return fmt.Errorf("the card says %d minutes, %d, %q", c.Minutes, c.Year, c.Why)
	}
	return nil
}

func (w *podcastWorld) cardButton(label, href string) error {
	c, err := w.one()
	if err != nil {
		return err
	}
	if c.Label != label || c.Href != href {
		return fmt.Errorf("the button is %q opening %q", c.Label, c.Href)
	}
	return nil
}

func (w *podcastWorld) writtenLast() error {
	if n := len(w.stub.requests()); n != 3 {
		return fmt.Errorf("the model was asked %d times, want 3 (search, show, write)", n)
	}
	if !strings.Contains(w.text(), "Apple Podcasts") {
		return fmt.Errorf("the words after the last tool call did not reach the page: %q", w.text())
	}
	return nil
}

func (w *podcastWorld) pageBuilds() error {
	js, err := os.ReadFile("static/js/chester.js")
	if err != nil {
		return err
	}
	for _, want := range []string{"podcastCard", "ch-podcast-open", "a.target = '_blank'", "noopener", "Open in Apple Podcasts"} {
		if !strings.Contains(string(js), want) {
			return fmt.Errorf("static/js/chester.js does not build the button: missing %q", want)
		}
	}
	return nil
}

func (w *podcastWorld) askedWith(words string) error {
	if len(w.queries) == 0 {
		return fmt.Errorf("the search service was never asked")
	}
	v, err := url.ParseQuery(w.queries[0])
	if err != nil {
		return err
	}
	if v.Get("media") != "podcast" || v.Get("entity") != "podcastEpisode" || v.Get("limit") != "25" || v.Get("term") != words {
		return fmt.Errorf("the search was asked %v, want the words %q", v, words)
	}
	return nil
}

func (w *podcastWorld) noCoordinate() error {
	bad := regexp.MustCompile(`\d\.\d`)
	for _, q := range w.queries {
		if dec, _ := url.QueryUnescape(q); bad.MatchString(dec) {
			return fmt.Errorf("a coordinate went to the search service: %s", dec)
		}
	}
	return nil
}

func (w *podcastWorld) toldDown() error {
	if !strings.Contains(w.toolResults(), "podcast search is unavailable") {
		return fmt.Errorf("the model was not told the search is unavailable: %s", w.toolResults())
	}
	return nil
}

func (w *podcastWorld) answerSaysDown() error {
	if !strings.Contains(w.text(), "podcast search is unavailable") {
		return fmt.Errorf("the answer does not say so: %q", w.text())
	}
	return nil
}

func (w *podcastWorld) loggedRow(ep string) error {
	rows, err := w.app.store.ChesterQueries(0)
	if err != nil || len(rows) != 1 {
		return fmt.Errorf("rows %d, err %v", len(rows), err)
	}
	if len(rows[0].Podcasts) != 1 || rows[0].Podcasts[0].Episode != ep {
		return fmt.Errorf("the row holds %+v", rows[0].Podcasts)
	}
	return nil
}

func (w *podcastWorld) bus(count int) error {
	for _, c := range w.fake.wait(w.t, 2) {
		if len(c) < 3 || !strings.Contains(c[2], "chester_podcast_search") {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(c[2]), &rec); err != nil {
			return err
		}
		if int(rec["count"].(float64)) != count || rec["status"].(float64) != 200 {
			return fmt.Errorf("record %v", rec)
		}
		if strings.Contains(c[2], "Tennessee") || strings.Contains(c[2], "TVA") {
			return fmt.Errorf("the query text is on the bus: %s", c[2])
		}
		return nil
	}
	return fmt.Errorf("no chester_podcast_search record on the bus")
}
