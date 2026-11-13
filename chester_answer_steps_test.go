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
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/chester_always_ends_in_an_answer.feature. The Messages
// API is the apiStub of chester_test.go and the library is libStub below,
// which matches the way MusicOrgan does: the whole search inside an album
// title or an artist name, case-blind, in album-title order, cut at the limit
// asked for before anything is said about whether an album can be played.
// Nothing here reaches Anthropic or a real library.

type libAlbum struct {
	album, artist string
	playable      bool
}

type libStub struct {
	srv    *httptest.Server
	albums []libAlbum
}

func newLibStub(t *testing.T) *libStub {
	s := &libStub{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/roadtracks/search" {
			http.NotFound(w, r)
			return
		}
		q := strings.ToLower(r.URL.Query().Get("q"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		type out struct {
			ID       int64  `json:"album_id"`
			Album    string `json:"album"`
			Artist   string `json:"albumartist"`
			URL      string `json:"url"`
			Playable bool   `json:"playable"`
		}
		var found []out
		for i, a := range s.albums {
			if strings.Contains(strings.ToLower(a.album), q) || strings.Contains(strings.ToLower(a.artist), q) {
				found = append(found, out{int64(i + 1), a.album, a.artist, fmt.Sprintf("%s/album/%d/", s.srv.URL, i+1), a.playable})
			}
		}
		sort.Slice(found, func(i, j int) bool { return strings.ToLower(found[i].Album) < strings.ToLower(found[j].Album) })
		if limit > 0 && len(found) > limit {
			found = found[:limit]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"albums": found, "artists": []any{}, "playlists": []any{}})
	}))
	t.Cleanup(s.srv.Close)
	return s
}

type answerWorld struct {
	t      *testing.T
	lib    *libStub
	on     bool
	env    map[string]string
	script []http.HandlerFunc
	stub   *apiStub
	app    *App
	evs    []chesterEvent
}

func initChesterAnswerScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &answerWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = answerWorld{t: t, env: map[string]string{}}
		return ctx, nil
	})
	sc.Step(`^the library to search holds "([^"]*)" by "([^"]*)", playable$`, func(album, artist string) error {
		w.library().albums = append(w.library().albums, libAlbum{album, artist, true})
		return nil
	})
	sc.Step(`^the library to search also holds (\d+) albums by "([^"]*)" that cannot be played$`, func(n int, artist string) error {
		for i := 1; i <= n; i++ {
			w.library().albums = append(w.library().albums, libAlbum{fmt.Sprintf("A Cash Record %02d", i), artist, false})
		}
		return nil
	})
	sc.Step(`^RoadTracks is on for Chester$`, func() error { w.on = true; return nil })
	sc.Step(`^the model searches the library for "([^"]*)" in every round it is given$`, func(q string) error {
		for range chesterRounds {
			w.search(q)
		}
		return nil
	})
	sc.Step(`^the model searches the library for "([^"]*)"$`, func(q string) error { w.search(q); return nil })
	sc.Step(`^the model then picks "([^"]*)"$`, w.pick)
	sc.Step(`^the model then writes "([^"]*)"$`, w.write)
	sc.Step(`^the backup model writes "([^"]*)"$`, w.write)
	sc.Step(`^the backup model setting is "([^"]*)"$`, func(v string) error { w.env["CHESTER_MODEL_BACKUP"] = v; return nil })
	sc.Step(`^the API answers the first model with 503 before the answer begins$`, w.down)
	sc.Step(`^the API answers the backup model with 503 too$`, w.down)
	sc.Step(`^the driver asks Drive Tracks for an album$`, func() error {
		return w.ask(url.Values{"source": {chesterSourceDrive}})
	})
	sc.Step(`^the trip-keeper asks Chester for "([^"]*)"$`, func(q string) error { return w.ask(url.Values{"q": {q}}) })
	sc.Step(`^the model was asked (\d+) times?$`, w.askedTimes)
	sc.Step(`^the searches before the last round were told there are no more searches$`, w.toldNoMore)
	sc.Step(`^the last request withdraws the search tool$`, w.withdrawn)
	sc.Step(`^the page shows a plain sentence saying the library was searched, and no error$`, w.plainSentence)
	sc.Step(`^the row has no error and says the model searched and picked no album$`, w.rowNoPick)
	sc.Step(`^the page shows one album card for "([^"]*)"$`, w.oneAlbum)
	sc.Step(`^the page shows "([^"]*)" and no error$`, w.showsNoError)
	sc.Step(`^request (\d+) asked the model "([^"]*)"$`, w.requestModel)
	sc.Step(`^the row's note says the first model was unavailable and the backup model answered$`, w.noteBackup)
	sc.Step(`^the row was served by "([^"]*)" and priced at that model$`, w.servedPriced)
	sc.Step(`^the page shows the error "([^"]*)" once$`, w.errorOnce)
}

func (w *answerWorld) library() *libStub {
	if w.lib == nil {
		w.lib = newLibStub(w.t)
	}
	return w.lib
}

func (w *answerWorld) search(q string) {
	n := strconv.Itoa(len(w.script))
	w.script = append(w.script, stream(events(evStart(stubModel, 100, 0, 0), evThinking(0),
		evClientTool(1, "toolu_q"+n, chesterSearchMusicTool, js(map[string]any{"query": q})), evEnd("tool_use", 20, 0, nil))...))
}

func (w *answerWorld) pick(ref string) error {
	in := js(map[string]any{"albums": []any{map[string]any{"ref": ref, "why": "It suits the road."}}})
	w.script = append(w.script, stream(events(evStart(stubModel, 120, 0, 0), evThinking(0),
		evClientTool(1, "toolu_p"+strconv.Itoa(len(w.script)), chesterShowMusicTool, in), evEnd("tool_use", 20, 0, nil))...))
	return nil
}

// write answers on whichever model the request named, as the API does.
func (w *answerWorld) write(text string) error {
	w.script = append(w.script, func(rw http.ResponseWriter, r *http.Request) {
		model := stubModel
		if req := w.stub.requests(); len(req) > 0 {
			var body struct{ Model string }
			_ = json.Unmarshal(req[len(req)-1].Body, &body)
			model = firstOf(body.Model, stubModel)
		}
		stream(events(evStart(model, 150, 0, 0), evText(0, text), evEnd("end_turn", 10, 0, nil))...)(rw, r)
	})
	return nil
}

// down is a 503 before any of the answer began, with the SDK told not to try
// again itself: its own retries are taken as already spent.
func (w *answerWorld) down() error {
	w.script = append(w.script, func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("x-should-retry", "false")
		apiError(http.StatusServiceUnavailable, "overloaded_error", "unavailable")(rw, r)
	})
	return nil
}

func (w *answerWorld) ask(form url.Values) error {
	w.stub = newAPIStub(w.t, w.script...)
	w.app = activeTripApp(w.t)
	w.app.chester = newChester(chesterEnv(w.t, w.stub, w.env))
	if w.on {
		w.app.tracks = newRoadtracks(rtKey, w.library().srv.URL)
	}
	rec := askForm(w.t, w.app, form)
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
		w.evs = append(w.evs, ev)
	}
	return nil
}

func (w *answerWorld) row() (ChesterQuery, error) {
	rows, err := w.app.store.ChesterQueries(0)
	if err != nil || len(rows) != 1 {
		return ChesterQuery{}, fmt.Errorf("want one row, got %d (%v)", len(rows), err)
	}
	return rows[0], nil
}

func (w *answerWorld) askedTimes(n int) error {
	if got := len(w.stub.requests()); got != n {
		return fmt.Errorf("the model was asked %d time(s), want %d", got, n)
	}
	return nil
}

// toolResultsOf is the text of the tool results request n sent back.
func (w *answerWorld) toolResultsOf(n int) string {
	var req struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(w.stub.requests()[n].Body, &req)
	var b strings.Builder
	for _, m := range req.Messages {
		if bytes.Contains(m.Content, []byte("tool_result")) {
			b.Write(m.Content)
		}
	}
	return b.String()
}

func (w *answerWorld) toldNoMore() error {
	reqs := w.stub.requests()
	if len(reqs) != chesterRounds {
		return fmt.Errorf("the model was asked %d time(s), want %d", len(reqs), chesterRounds)
	}
	last := w.toolResultsOf(len(reqs) - 1)
	if !strings.Contains(last, "No more searches") {
		return fmt.Errorf("the search result sent to the last round does not say there are no more searches: %s", last)
	}
	// The round before it was not yet the last, and was not told so.
	if mid := w.toolResultsOf(len(reqs) - 2); strings.Contains(mid, "No more searches") {
		return fmt.Errorf("a search before the next-to-last round was told there are no more: %s", mid)
	}
	return nil
}

func (w *answerWorld) withdrawn() error {
	reqs := w.stub.requests()
	last := reqs[len(reqs)-1]
	var body struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Tool struct {
					Name string `json:"name"`
				} `json:"tool"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(last.Body, &body); err != nil {
		return err
	}
	end := body.Messages[len(body.Messages)-1]
	removed := false
	for _, c := range end.Content {
		if c.Type == "tool_removal" && c.Tool.Name == chesterSearchMusicTool {
			removed = true
		}
	}
	if end.Role != "system" || !removed {
		return fmt.Errorf("the last request does not end with a system message withdrawing search_music: %s", string(last.Body[max(0, len(last.Body)-600):]))
	}
	if !strings.Contains(last.Header.Get("anthropic-beta"), string("mid-conversation-tool-changes-2026-07-01")) {
		return fmt.Errorf("the last request does not carry the tool-change beta: %q", last.Header.Get("anthropic-beta"))
	}
	// The tools list itself is never edited: that would break the cache and
	// the thinking sent back.
	var first struct {
		Tools json.RawMessage `json:"tools"`
	}
	var now struct {
		Tools json.RawMessage `json:"tools"`
	}
	_ = json.Unmarshal(reqs[0].Body, &first)
	_ = json.Unmarshal(last.Body, &now)
	if !bytes.Equal(first.Tools, now.Tools) {
		return fmt.Errorf("the tools list was edited between the first and the last request")
	}
	return nil
}

func (w *answerWorld) text() string {
	var b strings.Builder
	for _, e := range w.evs {
		if e.T == "text" {
			b.WriteString(e.D)
		}
	}
	return b.String()
}

func (w *answerWorld) errors() []string {
	var out []string
	for _, e := range w.evs {
		if e.T == "error" {
			out = append(out, e.D)
		}
	}
	return out
}

func (w *answerWorld) plainSentence() error {
	if errs := w.errors(); len(errs) > 0 {
		return fmt.Errorf("the page was shown an error: %v", errs)
	}
	if t := w.text(); !strings.Contains(t, "searched the music library") {
		return fmt.Errorf("the page was not told the library was searched: %q", t)
	}
	return nil
}

func (w *answerWorld) rowNoPick() error {
	r, err := w.row()
	if err != nil {
		return err
	}
	if r.Error != "" || !r.AnswerSynth || !strings.Contains(r.Note, "searched the music library") || !strings.Contains(r.Note, "picked no album") {
		return fmt.Errorf("row: error %q, synth %v, note %q", r.Error, r.AnswerSynth, r.Note)
	}
	return nil
}

func (w *answerWorld) oneAlbum(album string) error {
	var cards []chesterMusicCard
	for _, e := range w.evs {
		if e.T == "music" {
			cards = append(cards, e.Music...)
		}
	}
	if len(cards) != 1 || cards[0].Album != album {
		return fmt.Errorf("want one card for %q, got %+v (said %q, errors %v)", album, cards, w.text(), w.errors())
	}
	return nil
}

func (w *answerWorld) showsNoError(text string) error {
	if errs := w.errors(); len(errs) > 0 {
		return fmt.Errorf("the page was shown an error: %v", errs)
	}
	if got := w.text(); got != text {
		return fmt.Errorf("the page shows %q, want %q", got, text)
	}
	return nil
}

func (w *answerWorld) requestModel(n int, model string) error {
	reqs := w.stub.requests()
	if n < 1 || n > len(reqs) {
		return fmt.Errorf("there is no request %d of %d", n, len(reqs))
	}
	var body struct{ Model string }
	_ = json.Unmarshal(reqs[n-1].Body, &body)
	if body.Model != model {
		return fmt.Errorf("request %d asked %q, want %q", n, body.Model, model)
	}
	return nil
}

func (w *answerWorld) noteBackup() error {
	r, err := w.row()
	if err != nil {
		return err
	}
	for _, want := range []string{"the first model (" + stubModel + ") was unavailable (API 503)",
		"the backup model (claude-sonnet-5-5)", "the backup model answered (claude-sonnet-5-5)"} {
		if !strings.Contains(r.Note, want) {
			return fmt.Errorf("the row's note %q does not say %q", r.Note, want)
		}
	}
	if r.Error != "" {
		return fmt.Errorf("the row holds an error: %q", r.Error)
	}
	return nil
}

func (w *answerWorld) servedPriced(model string) error {
	r, err := w.row()
	if err != nil {
		return err
	}
	if r.ServedModel != model {
		return fmt.Errorf("the row was served by %q, want %q", r.ServedModel, model)
	}
	var reqs []chesterRequestUsage
	if err := json.Unmarshal(r.UsageJSON, &reqs); err != nil {
		return err
	}
	// The 503 ran nothing and is not charged: one request, asked of and
	// served by the backup model, and the row's dollars are its price there.
	if len(reqs) != 1 || reqs[0].Asked != model || reqs[0].Served != model {
		return fmt.Errorf("the usage record is %+v", reqs)
	}
	want := chesterUsageOf(reqs).cost()
	p, _ := chesterPriceFor(model)
	if !near(r.USD, want) || !near(want, 150*p.In/1e6+10*p.Out/1e6) {
		return fmt.Errorf("the row cost %v, want %v at the backup model's price", r.USD, want)
	}
	return nil
}

func (w *answerWorld) errorOnce(text string) error {
	errs := w.errors()
	if len(errs) != 1 || errs[0] != text {
		return fmt.Errorf("the page was shown %v, want %q once", errs, text)
	}
	return nil
}
