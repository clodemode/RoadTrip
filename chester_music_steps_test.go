package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/chester_music.feature. The Messages API is the apiStub
// and the library is the moStub of roadtracks_test.go: nothing here reaches
// Anthropic or a real MusicOrgan. "Chester will ..." steps script what the
// stubbed model does, one request each; "the trip-keeper asks" runs the real
// /chester/ask route and reads the streamed events back.
//
// The events are read into musicEvent, not chesterEvent, so the page's wire
// shape (a "music" event of cards with a Play href) is checked as the phone
// would read it.

type musicCard struct {
	Album  string `json:"album"`
	Artist string `json:"artist"`
	Year   int    `json:"year"`
	Why    string `json:"why"`
	Href   string `json:"href"`
	Label  string `json:"label"`
}

type musicEvent struct {
	T     string      `json:"t"`
	D     string      `json:"d"`
	Music []musicCard `json:"music"`
}

type musicWorld struct {
	t      *testing.T
	mo     *moStub
	on     bool
	script []http.HandlerFunc
	stub   *apiStub
	app    *App
	fake   *fakeRedis
	evs    []musicEvent
	raw    string
	moDown bool
}

func initChesterMusicScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &musicWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = musicWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^the music library holds "([^"]*)" by "([^"]*)" from (\d+)$`, w.library)
	sc.Step(`^RoadTracks is on$`, func() error { w.on = true; return nil })
	sc.Step(`^RoadTracks is off$`, func() error { w.on = false; return nil })
	sc.Step(`^the music library is down$`, func() error { w.moDown = true; return nil })
	sc.Step(`^Chester will search the library for "([^"]*)"$`, w.willSearch)
	sc.Step(`^Chester will search the library for "([^"]*)" and "([^"]*)" in one turn$`, w.willSearchTwo)
	sc.Step(`^Chester was told the library matches album and artist names, not moods or genres$`, w.toldNamesOnly)
	sc.Step(`^Chester will then show the album "([^"]*)" because "([^"]*)"$`, w.willShow)
	sc.Step(`^Chester will (?:then )?write "([^"]*)"$`, w.willWrite)
	sc.Step(`^the trip-keeper asks Chester "([^"]*)"$`, w.ask)
	sc.Step(`^the answer carries one album card for "([^"]*)" by "([^"]*)"$`, w.oneCard)
	sc.Step(`^the card says "([^"]*)"$`, w.cardSays)
	sc.Step(`^the card's Play link opens the album on the library$`, w.playOpens)
	sc.Step(`^the card's link carries no autoplay parameter$`, w.noAutoplay)
	sc.Step(`^the answer was written after the last tool call$`, w.writtenLast)
	sc.Step(`^the page builds Play as a new-tab link, at least 56 pixels tall and the width of the card$`, w.pageBuildsPlay)
	sc.Step(`^the answer carries no album card$`, w.noCard)
	sc.Step(`^Chester was offered no music tool$`, w.noMusicTool)
	sc.Step(`^the system prompt says nothing about the music library$`, w.promptSilent)
	sc.Step(`^Chester was told the music library is unavailable$`, w.toldUnavailable)
	sc.Step(`^nothing sent to the model or to the page holds the library key or address$`, w.noLeakAnywhere)
	sc.Step(`^nothing sent to the model holds the library key or address$`, w.noLeakToModel)
	sc.Step(`^the answer says the music library is unavailable$`, w.answerSaysDown)
	sc.Step(`^Chester was told the album "([^"]*)" by "([^"]*)" and its reference "([^"]*)"$`, w.toldAlbum)
	sc.Step(`^the bus carries a chester_music_search record with the query length (\d+) and the count (\d+) and not the query$`, w.bus)
}

func (w *musicWorld) library(album, artist, year string) error {
	w.mo = newMO(w.t)
	y, _ := strconv.Atoi(year)
	w.mo.body = fmt.Sprintf(`{"q":"x","albums":[{"album_id":42,"album":%q,"albumartist":%q,"year":%d,"tracks":17,"url":"BASE/album/42/"}],"artists":[],"playlists":[]}`,
		album, artist, y)
	return nil
}

// evClientTool is a client tool call, its input streamed in pieces.
func evClientTool(idx int, id, name, input string) []string {
	var deltas []map[string]any
	for len(input) > 0 {
		n := min(13, len(input))
		deltas = append(deltas, map[string]any{"type": "input_json_delta", "partial_json": input[:n]})
		input = input[n:]
	}
	return evBlock(idx, map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}, deltas...)
}

func (w *musicWorld) willSearch(q string) error {
	in := js(map[string]any{"query": q})
	w.script = append(w.script, stream(events(evStart(stubModel, 100, 0, 0), evThinking(0),
		evClientTool(1, "toolu_s"+strconv.Itoa(len(w.script)), "search_music", in), evEnd("tool_use", 20, 0, nil))...))
	return nil
}

// willSearchTwo is one model turn carrying two search_music calls: the moStub
// answers both with the same album, so the one reference m1 stands for it.
func (w *musicWorld) willSearchTwo(a, b string) error {
	n := strconv.Itoa(len(w.script))
	w.script = append(w.script, stream(events(evStart(stubModel, 100, 0, 0), evThinking(0),
		evClientTool(1, "toolu_a"+n, "search_music", js(map[string]any{"query": a})),
		evClientTool(2, "toolu_b"+n, "search_music", js(map[string]any{"query": b})),
		evEnd("tool_use", 20, 0, nil))...))
	return nil
}

func (w *musicWorld) toldNamesOnly() error {
	first := string(w.stub.requests()[0].Body)
	if !strings.Contains(first, "matches album and artist names only") {
		return fmt.Errorf("the model was not told the library matches names only")
	}
	if strings.Contains(first, "a mood or a genre") {
		return fmt.Errorf("the model is still told it can search a mood or a genre")
	}
	return nil
}

func (w *musicWorld) willShow(ref, why string) error {
	in := js(map[string]any{"albums": []any{map[string]any{"ref": ref, "why": why}}})
	w.script = append(w.script, stream(events(evStart(stubModel, 120, 0, 0), evThinking(0),
		evClientTool(1, "toolu_m"+strconv.Itoa(len(w.script)), "show_music", in), evEnd("tool_use", 20, 0, nil))...))
	return nil
}

func (w *musicWorld) willWrite(text string) error {
	w.script = append(w.script, stream(events(evStart(stubModel, 150, 0, 0), evText(0, text), evEnd("end_turn", 10, 0, nil))...))
	return nil
}

func (w *musicWorld) ask(q string) error {
	w.stub = newAPIStub(w.t, w.script...)
	w.app = chesterApp(w.t, w.stub)
	if w.on {
		if w.mo == nil {
			return fmt.Errorf("RoadTracks is on but the library was never stubbed")
		}
		if w.moDown {
			w.mo.status, w.mo.body = 503, `{"error":"down `+rtKey+`"}`
		}
		w.app.tracks = newRoadtracks(rtKey, w.mo.srv.URL)
	}
	w.fake = newFakeRedis(w.t, true)
	w.app.melt = newMelt(w.fake.addr(), "kate")
	rec := askForm(w.t, w.app, url.Values{"q": {q}})
	if rec.Code != http.StatusOK {
		return fmt.Errorf("ask: %d %s", rec.Code, rec.Body.String())
	}
	w.raw = rec.Body.String()
	sc := bufio.NewScanner(bytes.NewReader(rec.Body.Bytes()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ev musicEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return fmt.Errorf("a line of the answer is not JSON: %q", sc.Text())
		}
		w.evs = append(w.evs, ev)
	}
	return nil
}

func (w *musicWorld) cards() []musicCard {
	var out []musicCard
	for _, e := range w.evs {
		if e.T == "music" {
			out = append(out, e.Music...)
		}
	}
	return out
}

func (w *musicWorld) text() string {
	var b strings.Builder
	for _, e := range w.evs {
		if e.T == "text" {
			b.WriteString(e.D)
		}
	}
	return b.String()
}

func (w *musicWorld) oneCard(album, artist string) error {
	cs := w.cards()
	if len(cs) != 1 || cs[0].Album != album || cs[0].Artist != artist {
		return fmt.Errorf("want one card for %q by %q, got %+v (answer: %s)", album, artist, cs, w.raw)
	}
	return nil
}

func (w *musicWorld) cardSays(why string) error {
	if cs := w.cards(); len(cs) != 1 || cs[0].Why != why {
		return fmt.Errorf("the card does not say %q: %+v", why, cs)
	}
	return nil
}

func (w *musicWorld) playOpens() error {
	cs := w.cards()
	if len(cs) != 1 {
		return fmt.Errorf("want one card, got %d", len(cs))
	}
	if cs[0].Label != "Play" {
		return fmt.Errorf("the button is labelled %q, want exactly Play", cs[0].Label)
	}
	if want := w.mo.srv.URL + "/album/42/"; cs[0].Href != want {
		return fmt.Errorf("Play opens %q, want %q", cs[0].Href, want)
	}
	return nil
}

func (w *musicWorld) noAutoplay() error {
	cs := w.cards()
	if len(cs) != 1 {
		return fmt.Errorf("want one card, got %d", len(cs))
	}
	if u, err := url.Parse(cs[0].Href); err != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(strings.ToLower(cs[0].Href), "autoplay") {
		return fmt.Errorf("the Play link carries a query, fragment or autoplay: %q", cs[0].Href)
	}
	return nil
}

func (w *musicWorld) writtenLast() error {
	if n := len(w.stub.requests()); n != 3 {
		return fmt.Errorf("the model was asked %d times, want 3 (search, show, write)", n)
	}
	if !strings.Contains(w.text(), "Tap Play") {
		return fmt.Errorf("the model's words after the last tool call did not reach the page: %q", w.text())
	}
	return nil
}

func (w *musicWorld) pageBuildsPlay() error {
	js, err := os.ReadFile("static/js/chester.js")
	if err != nil {
		return err
	}
	css, err := os.ReadFile("static/css/app.css")
	if err != nil {
		return err
	}
	for _, want := range []string{"ch-play", "'Play'", "a.target = '_blank'", "noopener"} {
		if !strings.Contains(string(js), want) {
			return fmt.Errorf("static/js/chester.js does not build the Play link: missing %q", want)
		}
	}
	m := regexp.MustCompile(`\.ch-play\s*\{([^}]*)\}`).FindStringSubmatch(string(css))
	if m == nil {
		return fmt.Errorf("static/css/app.css has no .ch-play rule")
	}
	h := regexp.MustCompile(`min-height:\s*(\d+)px`).FindStringSubmatch(m[1])
	if h == nil {
		return fmt.Errorf(".ch-play has no min-height in px: %s", m[1])
	}
	if n, _ := strconv.Atoi(h[1]); n < 56 {
		return fmt.Errorf(".ch-play is %dpx tall, want at least 56", n)
	}
	if !strings.Contains(m[1], "display: flex") || !strings.Contains(m[1], "width: 100%") {
		return fmt.Errorf(".ch-play is not full card width: %s", m[1])
	}
	return nil
}

func (w *musicWorld) noCard() error {
	if cs := w.cards(); len(cs) != 0 {
		return fmt.Errorf("want no album card, got %+v", cs)
	}
	return nil
}

func (w *musicWorld) toolNames() []string {
	var req struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(w.stub.requests()[0].Body, &req)
	var out []string
	for _, t := range req.Tools {
		out = append(out, t.Name)
	}
	return out
}

func (w *musicWorld) noMusicTool() error {
	for _, n := range w.toolNames() {
		if strings.Contains(n, "music") {
			return fmt.Errorf("a music tool was offered with RoadTracks off: %v", w.toolNames())
		}
	}
	return nil
}

func (w *musicWorld) promptSilent() error {
	sys := string(w.stub.requests()[0].Body)
	for _, bad := range []string{"search_music", "show_music", "music library"} {
		if strings.Contains(sys, bad) {
			return fmt.Errorf("the request mentions %q with RoadTracks off", bad)
		}
	}
	return nil
}

// toolResults are the text of every tool_result the model was sent back.
func (w *musicWorld) toolResults() string {
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

func (w *musicWorld) toldUnavailable() error {
	if tr := w.toolResults(); !strings.Contains(tr, "music library is unavailable") {
		return fmt.Errorf("the model was not told the library is unavailable: %s", tr)
	}
	return nil
}

func (w *musicWorld) noLeakToModel() error {
	for i, r := range w.stub.requests() {
		b := string(r.Body)
		for _, bad := range []string{rtKey, w.mo.srv.URL, "127.0.0.1", "/album/"} {
			if strings.Contains(b, bad) {
				return fmt.Errorf("request %d to the model holds %q", i, bad)
			}
		}
	}
	return nil
}

func (w *musicWorld) noLeakAnywhere() error {
	if err := w.noLeakToModel(); err != nil {
		return err
	}
	for _, bad := range []string{rtKey, w.mo.srv.URL, "/album/"} {
		if strings.Contains(w.raw, bad) {
			return fmt.Errorf("the answer to the page holds %q", bad)
		}
	}
	return nil
}

func (w *musicWorld) answerSaysDown() error {
	if !strings.Contains(w.text(), "music library is unavailable") {
		return fmt.Errorf("the answer does not say so: %q", w.text())
	}
	return nil
}

func (w *musicWorld) toldAlbum(album, artist, ref string) error {
	tr := w.toolResults()
	for _, want := range []string{album, artist, ref} {
		if !strings.Contains(tr, want) {
			return fmt.Errorf("the search result sent to the model lacks %q: %s", want, tr)
		}
	}
	return nil
}

func (w *musicWorld) bus(qlen, count int) error {
	cmds := w.fake.wait(w.t, 2)
	for _, c := range cmds {
		if len(c) < 3 || !strings.Contains(c[2], "chester_music_search") {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(c[2]), &rec); err != nil {
			return err
		}
		if int(rec["qlen"].(float64)) != qlen || int(rec["count"].(float64)) != count {
			return fmt.Errorf("record %v", rec)
		}
		if strings.Contains(c[2], "abbey") {
			return fmt.Errorf("the query text is on the bus: %s", c[2])
		}
		return nil
	}
	return fmt.Errorf("no chester_music_search record on the bus: %v", cmds)
}
