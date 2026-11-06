package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// What the first live question showed, one section each: an answer that was
// a card and no words, dollars that did not follow from the row's own token
// counts, and one status said three times. Like every Chester test, nothing
// here reaches the real API or spends anything: the API is apiStub.

// --- A. an answer always has words ------------------------------------------------------

// cardsAlone is a turn that shows its places and writes nothing: thinking,
// the tool call, and the end.
func cardsAlone(id string) http.HandlerFunc {
	return stream(events(evStart(stubModel, 100, 0, 0), evThinking(0), evPlaces(1, id, placesInput), evEnd("tool_use", 30, 0, nil))...)
}

// silentEnd is a turn that ends properly having written nothing at all.
func silentEnd() http.HandlerFunc {
	return stream(events(evStart(stubModel, 150, 0, 0), evThinking(0), evEnd("end_turn", 5, 0, nil))...)
}

// exportRow reads the newest row out of the signed-in export as raw JSON, so
// a test sees the fields exactly as a reader of log.json does.
func exportRow(t *testing.T, app *App) map[string]json.RawMessage {
	t.Helper()
	var export struct {
		Queries []map[string]json.RawMessage `json:"queries"`
	}
	if err := json.Unmarshal(send(t, app, "GET", "/admin/chester/log.json", testToken, "", nil).Body.Bytes(), &export); err != nil {
		t.Fatal(err)
	}
	if len(export.Queries) == 0 {
		t.Fatal("the export has no row")
	}
	return export.Queries[0]
}

// The prompt asks for the cards first and the answer after them, and says
// why: the default model returns words written ahead of a tool call as an
// empty thinking block, so an answer written before its cards is never seen.
// It no longer asks for the answer first and nothing after the tool call,
// which on that model is an answer nobody receives.
func TestChesterPromptPutsTheAnswerAfterTheCards(t *testing.T) {
	stub := newAPIStub(t, quickScript())
	app := chesterApp(t, stub)
	_, r := askQ(t, app, url.Values{"preset": {"food"}})
	system := js(stub.request(0)["system"])
	for _, want := range []string{"before you write the answer", "after your last tool call",
		"words written before a tool call do not reach the traveller", "never instead of it",
		"at least one or two plain sentences"} {
		if !strings.Contains(system, want) {
			t.Errorf("the prompt does not say %q", want)
		}
	}
	for _, gone := range []string{"Write nothing after the tool call", "first write the whole answer", "give the answer first"} {
		if strings.Contains(system, gone) {
			t.Errorf("the prompt still says %q", gone)
		}
	}
	if desc := js(stub.request(0)["tools"]); !strings.Contains(desc, "before you write the answer") ||
		!strings.Contains(desc, "do not replace it") || strings.Contains(desc, "after the complete written answer") {
		t.Errorf("the tool's description does not put the cards before the answer: %s", desc)
	}
	if r.PromptVersion != "chester-rt-3" || chesterPromptVersion != "chester-rt-3" {
		t.Errorf("the prompt version is %q; the prompt changed, so it must have too", r.PromptVersion)
	}
	// Control: words that did arrive ahead of the cards are an answer, the
	// model's own, and the second request is not spent.
	if r.AnswerSynth || r.FirstMS == nil || len(stub.requests()) != 1 || r.Answer == "" {
		t.Errorf("a whole answer: synthesized %v, first word %v, %d request(s), answer %q", r.AnswerSynth, r.FirstMS, len(stub.requests()), r.Answer)
	}
}

// The live case: the cards came alone, the model was asked for its words and
// ended its turn without any. The page is never left with a card and nothing
// above it: it gets one fixed sentence the server built from the cards, and
// the row says the sentence is not the model's and that no word arrived.
func TestChesterNeverShowsCardsWithNoWords(t *testing.T) {
	stub := newAPIStub(t, cardsAlone("toolu_a"), silentEnd())
	app := chesterApp(t, stub)
	evs, r := askQ(t, app, url.Values{"preset": {"food"}})

	if n := len(stub.requests()); n != 2 {
		t.Fatalf("the API was asked %d time(s), want twice: once, and once more for the words", n)
	}
	// The follow-up is the tool's own answer, and it asks for the words.
	msgs := stub.request(1)["messages"].([]any)
	if result := js(msgs[len(msgs)-1]); !strings.Contains(result, `"type":"tool_result"`) ||
		!strings.Contains(result, "Nothing you wrote before this call was shown") || !strings.Contains(result, "Do not call show_places again") {
		t.Errorf("the follow-up does not ask for the words: %s", result)
	}

	const want = "Chester named 2 places and wrote nothing about them: Juniper Motel (Cedar Camp, ZZ); " +
		"Pump <b>& Go</b> (Birch Gate, ZZ). The cards below say why."
	if got := eventText(evs); got != want {
		t.Errorf("the page was given %q, want the fixed sentence %q", got, want)
	}
	if _, bad := eventOf(evs, "error"); bad {
		t.Errorf("cards with a sentence above them are not an error: %+v", evs)
	}
	if cards, _ := eventOf(evs, "places"); len(cards.Places) != 2 {
		t.Errorf("%d card(s) on the page, want 2", len(cards.Places))
	}
	if r.Answer != want || !r.AnswerSynth || r.Error != "" || r.StopReason != "end_turn" || len(r.Places) != 2 {
		t.Errorf("the row: answer %q, synthesized %v, error %q, stop %q, %d place(s)", r.Answer, r.AnswerSynth, r.Error, r.StopReason, len(r.Places))
	}
	if !strings.Contains(r.Note, "wrote no words") || !strings.Contains(r.Note, "built by the site") {
		t.Errorf("the row's note %q does not say whose sentence it is", r.Note)
	}
	// No word of the model's arrived, so there is no first-word time: empty,
	// in the row and in the export, and never a zero.
	if r.FirstMS != nil {
		t.Errorf("the row's first-word time is %d ms; no word arrived", *r.FirstMS)
	}
	raw := exportRow(t, app)
	if string(raw["first_ms"]) != "null" || string(raw["answer_synthesized"]) != "true" {
		t.Errorf("the export: first_ms %s, answer_synthesized %s; want null and true", raw["first_ms"], raw["answer_synthesized"])
	}
	var stored any
	if err := app.store.db.QueryRow(`SELECT first_ms FROM chester_queries WHERE id=?`, r.ID).Scan(&stored); err != nil || stored != nil {
		t.Errorf("the stored first_ms is %v (%v), want empty", stored, err)
	}
	// The log page says both, and renders the sentence as text.
	page := send(t, app, "GET", "/admin/chester/log", testToken, "", nil).Body.String()
	for _, w := range []string{"Not Chester's words", "<dd>no words</dd>", "Pump &lt;b&gt;&amp; Go&lt;/b&gt; (Birch Gate, ZZ)"} {
		if !strings.Contains(page, w) {
			t.Errorf("the log page lacks %q", w)
		}
	}
	if strings.Contains(page, "<dd>0 ms</dd>") {
		t.Error("the log page shows a first word at 0 ms")
	}
}

// Asked for its words, the model may show its cards again instead. That is
// one more round and no more, and a place listed twice is one card.
func TestChesterAsksForWordsAtMostTwice(t *testing.T) {
	stub := newAPIStub(t, cardsAlone("toolu_a"), cardsAlone("toolu_b"), silentEnd())
	app := chesterApp(t, stub)
	evs, r := askQ(t, app, url.Values{"preset": {"food"}})
	if n := len(stub.requests()); n != chesterRounds {
		t.Fatalf("the API was asked %d time(s), want %d and no more", n, chesterRounds)
	}
	if !r.AnswerSynth || len(r.Places) != 2 || !strings.HasPrefix(eventText(evs), "Chester named 2 places") {
		t.Errorf("the row: synthesized %v, %d place(s), text %q", r.AnswerSynth, len(r.Places), eventText(evs))
	}

	// One place reads differently: the sentence is the singular one.
	one := `{"places": [{"name": "Larch Lodge", "kind": "lodging", "town": "", "why": "Invented.", "url": "", "night": ""}]}`
	app = chesterApp(t, newAPIStub(t,
		stream(events(evStart(stubModel, 100, 0, 0), evPlaces(0, "toolu_one", one), evEnd("tool_use", 30, 0, nil))...), silentEnd()))
	if _, r := askQ(t, app, url.Values{"preset": {"lodging"}}); r.Answer != "Chester named one place and wrote nothing about it: Larch Lodge. The card below says why." {
		t.Errorf("one place alone reads %q", r.Answer)
	}

	// Control: asked for its words, the model writes them. The answer is its
	// own, with a first-word time, and nothing is synthesized.
	app = chesterApp(t, newAPIStub(t, cardsAlone("toolu_a"),
		stream(events(evStart(stubModel, 150, 0, 0), evText(0, "Both are on your way."), evEnd("end_turn", 10, 0, nil))...)))
	if _, r := askQ(t, app, url.Values{"preset": {"food"}}); r.Answer != "Both are on your way." || r.AnswerSynth || r.FirstMS == nil || *r.FirstMS <= 0 {
		t.Errorf("words on the second round: answer %q, synthesized %v, first word %v", r.Answer, r.AnswerSynth, r.FirstMS)
	}

	// Nothing at all, no words and no places, is still an error and never a
	// synthesized answer: there is nothing to build a sentence from.
	app = chesterApp(t, newAPIStub(t, silentEnd()))
	if _, r := askQ(t, app, url.Values{"preset": {"food"}}); r.Error != chesterSaidUnfinishd || r.AnswerSynth || r.Answer != "" || r.FirstMS != nil {
		t.Errorf("an empty turn: error %q, synthesized %v, answer %q, first word %v", r.Error, r.AnswerSynth, r.Answer, r.FirstMS)
	}
}

// A database from before first_ms could be empty: the zero it wrote for "no
// word arrived" was never a time, and becomes empty; a real time is kept.
func TestChesterFirstWordColumnIsMigrated(t *testing.T) {
	app := chesterApp(t, newAPIStub(t))
	db := app.store.db
	for _, q := range []string{
		// The old shape, rebuilt by hand.
		`ALTER TABLE chester_queries DROP COLUMN first_ms`,
		`ALTER TABLE chester_queries ADD COLUMN first_ms INTEGER NOT NULL DEFAULT 0`,
		`INSERT INTO chester_queries (asked_at,mode,question,model,first_ms,total_ms) VALUES ('2030-05-01T00:00:00Z','quick','no words','m',0,900)`,
		`INSERT INTO chester_queries (asked_at,mode,question,model,first_ms,total_ms) VALUES ('2030-05-01T00:01:00Z','quick','words','m',420,900)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Control: the old shape cannot hold an empty time, which is the defect.
	if _, err := db.Exec(`UPDATE chester_queries SET first_ms=NULL WHERE question='no words'`); err == nil {
		t.Fatal("the old shape took an empty first_ms; this test rebuilt the wrong shape")
	}
	for i := 0; i < 2; i++ { // twice: the second run finds nothing to do
		if err := app.store.migrate(); err != nil {
			t.Fatalf("migrate, run %d: %v", i+1, err)
		}
	}
	rows := chesterRows(t, app)
	if len(rows) != 2 || rows[0].Question != "words" || rows[0].FirstMS == nil || *rows[0].FirstMS != 420 ||
		rows[1].Question != "no words" || rows[1].FirstMS != nil {
		t.Errorf("after the migration: %+v", rows)
	}
	if string(rows[1].UsageJSON) != "[]" {
		t.Errorf("a row from before usage was kept has usage_json %s, want an empty list", rows[1].UsageJSON)
	}
}

// --- B. the row's tokens and dollars follow from the API's own usage --------------------

// refPrices are the list prices the reconcile test recomputes with. They are
// written out again here on purpose: the test must not borrow the arithmetic
// it is checking.
var refPrices = map[string][4]float64{ // in, out, cache write, cache read, per million
	"claude-opus-5-5": {4, 20, 5, 0.20},
	"claude-opus-4-8": {5, 25, 6.25, 0.50},
}
var refDearest = [4]float64{10, 50, 12.50, 1.00}

type refTotals struct {
	In, Out, CacheRead, CacheWrite, Searches int64
	USD                                      float64
	Estimated                                bool
}

func refPrice(models ...string) [4]float64 {
	var p [4]float64
	for _, m := range models {
		q, ok := refPrices[m]
		if !ok {
			q = refDearest
		}
		for i := range p {
			p[i] = max(p[i], q[i])
		}
	}
	return p
}

// recompute works a row's totals out again from its usage_json, reading the
// stored list as plain JSON and applying the rules as the README states them.
func recompute(t *testing.T, usageJSON []byte) refTotals {
	t.Helper()
	var reqs []struct {
		Asked     string         `json:"asked"`
		Served    string         `json:"served"`
		MaxTokens int64          `json:"max_tokens"`
		Finished  bool           `json:"finished"`
		Start     map[string]any `json:"start"`
		Final     map[string]any `json:"final"`
	}
	if err := json.Unmarshal(usageJSON, &reqs); err != nil {
		t.Fatalf("usage_json is not a list of requests: %v: %s", err, usageJSON)
	}
	num := func(m map[string]any, k string) (int64, bool) {
		f, ok := m[k].(float64)
		return int64(f), ok
	}
	var out refTotals
	add := func(p [4]float64, in, o, cw, cr int64) {
		out.In, out.Out, out.CacheWrite, out.CacheRead = out.In+in, out.Out+o, out.CacheWrite+cw, out.CacheRead+cr
		out.USD += (float64(in)*p[0] + float64(o)*p[1] + float64(cw)*p[2] + float64(cr)*p[3]) / 1e6
	}
	for _, r := range reqs {
		served := r.Served
		if served == "" {
			served = r.Asked
		}
		both := refPrice(r.Asked, served)
		if !r.Finished {
			in, _ := num(r.Start, "input_tokens")
			cw, _ := num(r.Start, "cache_creation_input_tokens")
			cr, _ := num(r.Start, "cache_read_input_tokens")
			add(both, in, 0, cw, cr)
			out.USD += float64(r.MaxTokens) * both[1] / 1e6
			out.Estimated = true
			continue
		}
		if stu, ok := r.Final["server_tool_use"].(map[string]any); ok {
			n, _ := num(stu, "web_search_requests")
			out.Searches += n
			out.USD += float64(n) * 0.01
		}
		if its, _ := r.Final["iterations"].([]any); len(its) > 0 {
			for _, raw := range its {
				it := raw.(map[string]any)
				p := refDearest
				if model, _ := it["model"].(string); model != "" {
					p = refPrice(model)
				} else if kind := it["type"]; kind == "message" || kind == "fallback_message" {
					p = both
				}
				in, _ := num(it, "input_tokens")
				o, _ := num(it, "output_tokens")
				cw, _ := num(it, "cache_creation_input_tokens")
				cr, _ := num(it, "cache_read_input_tokens")
				add(p, in, o, cw, cr)
			}
			continue
		}
		pick := func(k string) int64 {
			if v, ok := num(r.Final, k); ok {
				return v
			}
			v, _ := num(r.Start, k)
			return v
		}
		o, _ := num(r.Final, "output_tokens")
		add(both, pick("input_tokens"), o, pick("cache_creation_input_tokens"), pick("cache_read_input_tokens"))
	}
	return out
}

// searchLoopUsage is the final usage of a request that ran one web search,
// shaped as the live API sent it: the top-level counts are the whole request,
// and the itemised list gives the same tokens step by step, each step naming
// no model. The numbers are the first live question's.
func searchLoopUsage() map[string]any {
	return map[string]any{
		"input_tokens": 2271, "output_tokens": 714, "cache_creation_input_tokens": 9012, "cache_read_input_tokens": 22786,
		"server_tool_use": map[string]any{"web_search_requests": 1, "web_fetch_requests": 0},
		"iterations": []any{
			iteration("message", "", 1200, 150, 5600, 0),
			iteration("message", "", 600, 264, 3412, 5600),
			iteration("message", "", 471, 300, 0, 17186),
		},
	}
}

// The log is the experiment's instrument: every row keeps the API's own
// usage for each request it took, as it arrived, and the row's tokens,
// searches and dollars are that record worked out, nothing more and nothing
// less. This test works them out again, independently, for every shape the
// stub can send.
func TestChesterRowReconcilesWithItsUsage(t *testing.T) {
	loop := searchLoopUsage()
	cut := stream(events(evStart(stubModel, 200, 0, 0), evText(0, "Fuel is about "))...)
	for _, c := range []struct {
		name   string
		script []http.HandlerFunc
		form   url.Values
		// What the row must come to, said outright as well as recomputed.
		in, out, cacheRead, cacheWrite, searches int64
		usd                                      float64
		estimated                                bool
		// A final usage object the stored record must hold as it was sent.
		final map[string]any
	}{
		{name: "one plain request: the first event's counts and the last one's",
			script: []http.HandlerFunc{quickScript()}, form: url.Values{"preset": {"fuel"}},
			in: 200, out: 50, cacheWrite: 900, usd: (200*4 + 50*20 + 900*5) / 1e6,
			final: map[string]any{"output_tokens": 50}},
		{name: "a search loop itemised with no model named, as the live API sends it",
			script: []http.HandlerFunc{stream(events(evStart(stubModel, 1200, 5600, 0), evSearch(0, "srvtoolu_x"),
				evText(2, "There is a place ahead."), evEndWith("end_turn", loop))...)},
			form: url.Values{"preset": {"food"}},
			in:   2271, out: 714, cacheRead: 22786, cacheWrite: 9012, searches: 1,
			usd:   (2271*4+714*20+9012*5)/1e6 + 22786*0.20/1e6 + 0.01,
			final: loop},
		{name: "two requests: the cards alone, then the words",
			script: []http.HandlerFunc{cardsAlone("toolu_a"),
				stream(events(evStart(stubModel, 150, 0, 0), evText(0, "Both are on your way."), evEnd("end_turn", 10, 0, nil))...)},
			form: url.Values{"preset": {"food"}},
			in:   250, out: 40, usd: (250*4 + 40*20) / 1e6},
		{name: "a fallback: each attempt at the price of the model that ran it",
			script: []http.HandlerFunc{fallbackScript([]any{iteration("message", stubModel, 1000, 0, 900, 0),
				iteration("fallback_message", "claude-opus-4-8", 1000, 50, 0, 0)})},
			form: url.Values{"q": {"Is the pass open?"}},
			in:   2000, out: 50, cacheWrite: 900, usd: (1000*4+900*5)/1e6 + (1000*5+50*25)/1e6},
		{name: "an attempt on a model the table does not know: the dearest price",
			script: []http.HandlerFunc{fallbackScript([]any{iteration("message", "some-future-model", 1000, 10, 0, 0),
				iteration("fallback_message", "claude-opus-4-8", 1000, 50, 0, 0)})},
			form: url.Values{"q": {"Is the pass open?"}},
			in:   2000, out: 60, usd: (1000*10+10*50)/1e6 + (1000*5+50*25)/1e6},
		{name: "a request that was cut off: what it read, and the most it could have written",
			script: []http.HandlerFunc{cut}, form: url.Values{"preset": {"fuel"}},
			in: 200, usd: (200*4 + 8000*20) / 1e6, estimated: true},
		{name: "a request the API would not take: nothing ran, nothing is kept",
			script: []http.HandlerFunc{apiError(http.StatusBadRequest, "invalid_request_error", "no")},
			form:   url.Values{"preset": {"fuel"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			app := chesterApp(t, newAPIStub(t, c.script...))
			evs, r := askQ(t, app, c.form)
			raw := exportRow(t, app)

			// The row against what this case is known to come to.
			if r.TokensIn != c.in || r.TokensOut != c.out || r.CacheRead != c.cacheRead || r.CacheWrite != c.cacheWrite ||
				r.Searches != c.searches || !near(r.USD, c.usd) || r.USDEstimated != c.estimated {
				t.Errorf("the row: %d in, %d out, %d read, %d written, %d search(es), $%v (estimate %v); want %d, %d, %d, %d, %d, $%v (estimate %v)",
					r.TokensIn, r.TokensOut, r.CacheRead, r.CacheWrite, r.Searches, r.USD, r.USDEstimated,
					c.in, c.out, c.cacheRead, c.cacheWrite, c.searches, c.usd, c.estimated)
			}
			// The row against its own usage_json, worked out again from scratch.
			got := recompute(t, raw["usage_json"])
			if got.In != r.TokensIn || got.Out != r.TokensOut || got.CacheRead != r.CacheRead || got.CacheWrite != r.CacheWrite ||
				got.Searches != r.Searches || !near(got.USD, r.USD) || got.Estimated != r.USDEstimated {
				t.Errorf("the row does not follow from its usage_json: recomputed %+v, the row has %d in, %d out, %d read, %d written, %d search(es), $%v (estimate %v)\n%s",
					got, r.TokensIn, r.TokensOut, r.CacheRead, r.CacheWrite, r.Searches, r.USD, r.USDEstimated, raw["usage_json"])
			}
			// The page was told the same dollars the row holds.
			if done, _ := eventOf(evs, "done"); !near(done.USD, r.USD) || done.Est != r.USDEstimated {
				t.Errorf("the page was told $%v (estimate %v); the row holds $%v (estimate %v)", done.USD, done.Est, r.USD, r.USDEstimated)
			}
			// The record is the API's own object, as it was sent.
			if c.final != nil {
				var reqs []struct {
					Final json.RawMessage `json:"final"`
				}
				if err := json.Unmarshal(raw["usage_json"], &reqs); err != nil || len(reqs) != 1 {
					t.Fatalf("usage_json: %v: %s", err, raw["usage_json"])
				}
				var kept, sent any
				_ = json.Unmarshal(reqs[0].Final, &kept)
				_ = json.Unmarshal([]byte(js(c.final)), &sent)
				if js(kept) != js(sent) {
					t.Errorf("the stored final usage is not what the API sent:\n kept %s\n sent %s", js(kept), js(sent))
				}
			}
			// Usage only: nothing of the question, the answer or a place.
			for _, never := range []string{"Birch Gate", "Juniper", "pass is open", "on your way", "today's route", "Fuel is about"} {
				if strings.Contains(string(raw["usage_json"]), never) {
					t.Errorf("usage_json carries %q; it is counts and nothing else: %s", never, raw["usage_json"])
				}
			}
		})
	}
}

// The first live question, as numbers: its tokens at the model's own prices
// come to about eight cents. It was logged at twenty, because the itemised
// steps name no model and each was priced as the dearest model in the table.
func TestChesterASearchLoopIsPricedAtTheModelThatRanIt(t *testing.T) {
	final, _ := json.Marshal(searchLoopUsage())
	u := chesterUsageOf([]chesterRequestUsage{{Asked: stubModel, Served: stubModel, MaxTokens: 8000, Finished: true, Final: final}})
	const want = (2271*4+714*20+9012*5)/1e6 + 22786*0.20/1e6 + 0.01 // $0.0829812
	if !near(u.cost(), want) || u.In != 2271 || u.Out != 714 || u.CacheRead != 22786 || u.CacheWrite != 9012 || u.Searches != 1 {
		t.Errorf("the search loop came to $%v with %+v, want $%v", u.cost(), u, want)
	}
	if dearest := (2271*10+714*50+9012*12.5+22786*1.0)/1e6 + 0.01; near(u.cost(), dearest) {
		t.Errorf("the search loop was priced at $%v, the dearest model in the table", u.cost())
	}
	// The itemised steps and the top-level counts are the same tokens: read
	// either way, a request that was not served by a fallback costs the same.
	var top map[string]any
	_ = json.Unmarshal(final, &top)
	delete(top, "iterations")
	topOnly, _ := json.Marshal(top)
	if v := chesterUsageOf([]chesterRequestUsage{{Asked: stubModel, Served: stubModel, Finished: true, Final: topOnly}}); !near(v.cost(), want) {
		t.Errorf("the top-level counts alone come to $%v, want the same $%v", v.cost(), want)
	}

	// What still takes the dearest price: an entry of another kind that names
	// no model, and a model the table does not know.
	other, _ := json.Marshal(map[string]any{"output_tokens": 0, "iterations": []any{iteration("compaction", "", 1000, 100, 0, 0)}})
	if v := chesterUsageOf([]chesterRequestUsage{{Asked: stubModel, Finished: true, Final: other}}); !near(v.cost(), (1000*10+100*50)/1e6) {
		t.Errorf("an entry of another kind with no model came to $%v", v.cost())
	}
	// A final usage object that cannot be read is not counted as nothing: the
	// request is charged its ceiling.
	if v := chesterUsageOf([]chesterRequestUsage{{Asked: stubModel, MaxTokens: 8000, Finished: true,
		Final: json.RawMessage(`{"input_tokens": "many"}`)}}); !near(v.cost(), 8000*20/1e6) || v.CeilingUSD == 0 {
		t.Errorf("an unreadable final usage came to $%v (ceiling $%v), want the ceiling", v.cost(), v.CeilingUSD)
	}
	// The stored form reads back to the same figures.
	stored := chesterUsageJSON([]chesterRequestUsage{{Asked: stubModel, Served: stubModel, MaxTokens: 8000, Finished: true, Final: final}})
	if back, err := chesterUsageFromJSON(stored); err != nil || !near(back.cost(), want) {
		t.Errorf("read back from its stored form: $%v, %v", back.cost(), err)
	}
	if empty, err := chesterUsageFromJSON(chesterUsageJSON(nil)); err != nil || empty.cost() != 0 {
		t.Errorf("an empty record: $%v, %v", empty.cost(), err)
	}
	if _, err := chesterUsageFromJSON("not json"); err == nil {
		t.Error("a record that is not JSON was read without an error")
	}
}

// --- C. one status is said once ----------------------------------------------------------

func statusLines(evs []chesterEvent) []string {
	var out []string
	for _, e := range evs {
		if e.T == "status" {
			out = append(out, e.D)
		}
	}
	return out
}

// evServerStep is a step the API runs around a web search: a server tool
// call that is not the search itself, and its result.
func evServerStep(idx int, id string) []string {
	out := evBlock(idx, map[string]any{"type": "server_tool_use", "id": id, "name": "code_execution", "input": map[string]any{}})
	return append(out, evBlock(idx+1, map[string]any{"type": "code_execution_tool_result", "tool_use_id": id,
		"content": map[string]any{"type": "code_execution_result", "stdout": "", "stderr": "", "return_code": 0, "content": []any{}}})...)
}

// The live stream said "Searching the web" three times for one search and
// "Thinking" twice in a row. Each is said once; a status that comes back
// after something else was said is said again.
func TestChesterSaysAStatusOnce(t *testing.T) {
	stub := newAPIStub(t, stream(events(
		evStart(stubModel, 100, 0, 0),
		evThinking(0),
		evServerStep(1, "srvtoolu_a"), evSearch(3, "srvtoolu_b"), evServerStep(5, "srvtoolu_c"),
		evThinking(7), evThinking(8),
		evText(9, "There is a place ahead. "),
		evSearch(10, "srvtoolu_d"),
		evText(12, "It is open."),
		evEnd("end_turn", 60, 2, nil),
	)...))
	app := chesterApp(t, stub)
	evs, r := askQ(t, app, url.Values{"preset": {"food"}})
	want := []string{"Thinking", "Searching the web", "Thinking", "Searching the web"}
	if got := statusLines(evs); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("the status lines are %q, want %q", got, want)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].T == "status" && evs[i-1].T == "status" && evs[i].D == evs[i-1].D {
			t.Errorf("the same status twice in a row: %q", evs[i].D)
		}
	}
	// Control: collapsing a status never drops a word of the answer.
	if eventText(evs) != "There is a place ahead. \n\nIt is open." || r.Searches != 2 || r.Error != "" {
		t.Errorf("the answer: %q, %d search(es), error %q", eventText(evs), r.Searches, r.Error)
	}
}
