package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The review fixes on Chester, one section each. Like every Chester test,
// nothing here reaches the real API or spends anything: the API is apiStub.

// evEndWith ends a streamed message with the usage block given whole, for the
// cases the plain evEnd cannot say (an itemised list of attempts).
func evEndWith(stop string, usage map[string]any) []string {
	return []string{
		js(map[string]any{"type": "message_delta", "usage": usage,
			"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil, "stop_details": nil}}),
		js(map[string]any{"type": "message_stop"}),
	}
}

func askQ(t *testing.T, app *App, form url.Values) ([]chesterEvent, ChesterQuery) {
	t.Helper()
	evs := askEvents(t, askForm(t, app, form))
	rows := chesterRows(t, app)
	if len(rows) == 0 {
		t.Fatal("the question wrote no row")
	}
	return evs, rows[0]
}

// --- 1. a stream that is cut off is not a finished answer --------------------------

// The API began to answer and the stream closed with no message_delta: no
// stop reason, no final count. That is an error the page and the row both
// say plainly, and the round is charged the most it could have written.
func TestChesterACutStreamIsNotAnAnswer(t *testing.T) {
	cut := stream(events(evStart(stubModel, 200, 0, 0), evText(0, "Fuel is about "))...)
	stub := newAPIStub(t, cut, quickScript(), cut,
		stream(events(evStart(stubModel, 100, 0, 0), evText(0, "Still looking. "), evSearch(1, "srvtoolu_p"), evEnd("pause_turn", 20, 1, nil))...),
		stream(events(evStart(stubModel, 300, 0, 0), evText(0, "Found"))...))
	app := chesterApp(t, stub)

	evs, r := askQ(t, app, url.Values{"preset": {"fuel"}})
	// The words that arrived were streamed, and then taken back.
	if eventText(evs) != "Fuel is about " {
		t.Fatalf("the streamed text is %q; the stub's partial did not arrive, so this test is about nothing", eventText(evs))
	}
	said, ok := eventOf(evs, "error")
	if !ok || said.D != chesterSaidDropped || !said.Drop {
		t.Errorf("the page was told %+v, want %q and to drop the partial", said, chesterSaidDropped)
	}
	if !strings.Contains(chesterSaidDropped, "The answer was cut off") {
		t.Errorf("the sentence %q does not say the answer was cut off", chesterSaidDropped)
	}
	// 200 tokens read at $4 a million, and the round's ceiling: the mode's
	// 8,000 max_tokens at $20 a million.
	const wantQuick = (200*4 + 8000*20) / 1e6
	done, _ := eventOf(evs, "done")
	if !done.Est || !near(done.USD, wantQuick) || done.Stop != "cut_off" || done.ID != r.ID {
		t.Errorf("the last event: %+v, want an estimate of %v", done, wantQuick)
	}
	if r.Error != chesterSaidDropped || r.StopReason != "" || r.Answer != "" || r.Partial != "Fuel is about" {
		t.Errorf("the row: error %q, stop %q, answer %q, partial %q", r.Error, r.StopReason, r.Answer, r.Partial)
	}
	if !r.USDEstimated || !near(r.USD, wantQuick) || r.TokensIn != 200 || r.TokensOut != 0 {
		t.Errorf("the row's cost: $%v (estimate %v), %d in, %d out; want $%v as an estimate, and no invented output count",
			r.USD, r.USDEstimated, r.TokensIn, r.TokensOut, wantQuick)
	}
	for _, want := range []string{"ended: cut_off", "the cost is an estimate", "$0.1600"} {
		if !strings.Contains(r.Note, want) {
			t.Errorf("the row's note %q does not say %q", r.Note, want)
		}
	}
	// The cap sees the ceiling.
	if spent, _ := app.store.ChesterSpent(time.Now()); !near(spent, wantQuick) {
		t.Errorf("today's spend reads %v, want the ceiling %v", spent, wantQuick)
	}

	// Control: a whole answer is not an estimate, on the page or in the row.
	evs, whole := askQ(t, app, url.Values{"preset": {"fuel"}})
	if done, _ := eventOf(evs, "done"); done.Est || whole.USDEstimated || whole.Answer == "" || whole.Partial != "" || whole.Error != "" {
		t.Errorf("a whole answer: estimate %v/%v, answer %q, partial %q, error %q", done.Est, whole.USDEstimated, whole.Answer, whole.Partial, whole.Error)
	}

	// Plan mode's ceiling is its own max_tokens: 32,000 at $20 a million.
	_, plan := askQ(t, app, url.Values{"mode": {"plan"}, "q": {"Where tomorrow?"}})
	if want := (200*4 + 32000*20) / 1e6; !plan.USDEstimated || !near(plan.USD, want) {
		t.Errorf("a cut plan question cost $%v (estimate %v), want $%v", plan.USD, plan.USDEstimated, want)
	}

	// A first round that ended properly is counted as it was; only the round
	// that was cut is charged the ceiling.
	_, two := askQ(t, app, url.Values{"preset": {"stop"}})
	if want := (100*4+20*20)/1e6 + 0.01 + (300*4+8000*20)/1e6; !two.USDEstimated || !near(two.USD, want) ||
		two.TokensIn != 400 || two.TokensOut != 20 || two.Searches != 1 || two.Partial != "Still looking. \n\nFound" {
		t.Errorf("a question cut in its second round: $%v (estimate %v), %d in, %d out, %d search(es), partial %q; want $%v",
			two.USD, two.USDEstimated, two.TokensIn, two.TokensOut, two.Searches, two.Partial, want)
	}

	// The log page and the export say which costs are estimates.
	page := send(t, app, "GET", "/driver/chester/log", testToken, "", nil).Body.String()
	if n := strings.Count(page, "(an estimate)"); n != 3 {
		t.Errorf("the log page marks %d cost(s) as an estimate, want 3 of the 4", n)
	}
	if !strings.Contains(page, "$0.1608 (an estimate)") || !strings.Contains(page, chesterSaidDropped) {
		t.Error("the log page does not show the ceiling as an estimate beside the cut-off sentence")
	}
	var export struct {
		Queries []ChesterQuery `json:"queries"`
	}
	if err := json.Unmarshal(send(t, app, "GET", "/driver/chester/log.json", testToken, "", nil).Body.Bytes(), &export); err != nil {
		t.Fatal(err)
	}
	estimates := 0
	for _, q := range export.Queries {
		if q.USDEstimated {
			estimates++
		}
	}
	if estimates != 3 {
		t.Errorf("the export marks %d cost(s) as an estimate, want 3", estimates)
	}
}

// What is and is not charged when a round has no final count.
func TestChesterChargesOnlyWhatMayHaveRun(t *testing.T) {
	// The API said no before anything ran: nothing is charged.
	stub := newAPIStub(t, apiError(http.StatusBadRequest, "invalid_request_error", "no"),
		// It began, then failed part way: it ran, so the ceiling is charged.
		stream(events(evStart(stubModel, 80, 0, 0), evText(0, "The pass "),
			js(map[string]any{"type": "error", "error": map[string]any{"type": "overloaded_error", "message": "Overloaded"}}))...))
	app := chesterApp(t, stub)
	_, refused := askQ(t, app, url.Values{"q": {"Is the pass open?"}})
	if refused.USD != 0 || refused.USDEstimated || !strings.Contains(refused.Note, "ended: api_400") {
		t.Errorf("a request the API would not take: $%v (estimate %v), note %q", refused.USD, refused.USDEstimated, refused.Note)
	}
	_, midway := askQ(t, app, url.Values{"q": {"Is the pass open?"}})
	if want := (80*4 + 8000*20) / 1e6; !midway.USDEstimated || !near(midway.USD, want) || midway.Error == "" || midway.Answer != "" {
		t.Errorf("an error part way through: $%v (estimate %v), error %q, answer %q; want $%v", midway.USD, midway.USDEstimated, midway.Error, midway.Answer, want)
	}

	// A connection that was never opened sent nothing; any other failure may
	// have, and is not excused.
	dial := &url.Error{Op: "Post", URL: "http://stub.invalid", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
	read := &url.Error{Op: "Post", URL: "http://stub.invalid", Err: &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}}
	if !chesterNeverSent(dial) || !chesterNeverSent(fmt.Errorf("wrapped: %w", dial)) {
		t.Error("a failed dial was not recognised as never sent")
	}
	if chesterNeverSent(read) || chesterNeverSent(errors.New("something else")) || chesterNeverSent(nil) {
		t.Error("a failure after the connection opened was excused as never sent")
	}
}

// --- 2. a fallback is billed for every attempt -----------------------------------------

// fallbackScript is a question the first model declined before writing and a
// second model answered: message_start names the second model, a fallback
// block marks the switch, and usage.iterations itemises both attempts while
// the top-level counts cover only the one that answered.
func fallbackScript(iterations []any) http.HandlerFunc {
	return stream(events(
		evStart("claude-opus-4-8", 1000, 0, 0),
		evBlock(0, map[string]any{"type": "fallback", "from": map[string]any{"model": stubModel}, "to": map[string]any{"model": "claude-opus-4-8"}}),
		evText(1, "The pass is open."),
		evEndWith("end_turn", map[string]any{"input_tokens": 1000, "output_tokens": 50, "iterations": iterations}),
	)...)
}

func iteration(kind, model string, in, out, cacheWrite, cacheRead int) map[string]any {
	it := map[string]any{"type": kind, "input_tokens": in, "output_tokens": out,
		"cache_creation_input_tokens": cacheWrite, "cache_read_input_tokens": cacheRead,
		"cache_creation": map[string]any{"ephemeral_5m_input_tokens": cacheWrite, "ephemeral_1h_input_tokens": 0}}
	if model != "" {
		it["model"] = model
	}
	return it
}

func TestChesterFallbackCountsEveryAttempt(t *testing.T) {
	for _, c := range []struct {
		name       string
		iterations []any
		wantUSD    float64
		in, out    int64
	}{
		// The declined attempt read 1,000 tokens and wrote 900 to the cache on
		// the model asked ($4 and $5 a million); the fallback read 1,000 and
		// wrote 50 on the dearer model ($5 and $25).
		{"each attempt at its own model's price",
			[]any{iteration("message", stubModel, 1000, 0, 900, 0), iteration("fallback_message", "claude-opus-4-8", 1000, 50, 0, 0)},
			(1000*4+900*5)/1e6 + (1000*5+50*25)/1e6, 2000, 50},
		// A model the table does not know takes the dearest price of any it
		// does: $10 in and $50 out.
		{"an attempt on a model the table does not know",
			[]any{iteration("message", "some-future-model", 1000, 10, 0, 0), iteration("fallback_message", "claude-opus-4-8", 1000, 50, 0, 0)},
			(1000*10+10*50)/1e6 + (1000*5+50*25)/1e6, 2000, 60},
		// An entry that names no model at all is priced the same way.
		{"an entry that names no model",
			[]any{iteration("compaction", "", 2000, 100, 0, 0), iteration("fallback_message", "claude-opus-4-8", 1000, 50, 0, 0)},
			(2000*10+100*50)/1e6 + (1000*5+50*25)/1e6, 3000, 150},
	} {
		t.Run(c.name, func(t *testing.T) {
			app := chesterApp(t, newAPIStub(t, fallbackScript(c.iterations)))
			evs, r := askQ(t, app, url.Values{"q": {"Is the pass open?"}})
			if !near(r.USD, c.wantUSD) || r.TokensIn != c.in || r.TokensOut != c.out {
				t.Errorf("the row: $%v, %d in, %d out; want $%v, %d in, %d out", r.USD, r.TokensIn, r.TokensOut, c.wantUSD, c.in, c.out)
			}
			// The top-level counts alone, at the dearer model, are what the
			// row must NOT be: that is the attempt that answered and no other.
			if topOnly := (1000*5 + 50*25) / 1e6; near(r.USD, topOnly) {
				t.Errorf("the row cost $%v, which is the answering attempt alone", r.USD)
			}
			if done, _ := eventOf(evs, "done"); !near(done.USD, c.wantUSD) || done.Est {
				t.Errorf("the page was told $%v (estimate %v)", done.USD, done.Est)
			}
			if r.ServedModel != "claude-opus-4-8" || r.Answer != "The pass is open." || r.USDEstimated ||
				!strings.Contains(r.Note, "the first model declined and another answered") {
				t.Errorf("the row: served by %q, answer %q, estimate %v, note %q", r.ServedModel, r.Answer, r.USDEstimated, r.Note)
			}
		})
	}
	// Control: with no itemised list the top-level counts are all there is,
	// priced at the dearer of the model asked and the one that answered.
	app := chesterApp(t, newAPIStub(t, stream(events(evStart("claude-opus-4-8", 1000, 0, 0), evText(0, "Open."), evEnd("end_turn", 50, 0, nil))...)))
	if _, r := askQ(t, app, url.Values{"q": {"Is the pass open?"}}); !near(r.USD, (1000*5+50*25)/1e6) || r.TokensIn != 1000 {
		t.Errorf("without an itemised list: $%v, %d in", r.USD, r.TokensIn)
	}
}

// --- 3. the daily limit has a ceiling of its own ---------------------------------------

func TestChesterDailyLimitHasACeiling(t *testing.T) {
	read := func(usd string) (*chesterConfig, string) {
		env := map[string]string{"ANTHROPIC_API_KEY": stubKey, "CHESTER_DAILY_USD": usd}
		return chesterConfigFrom(func(k string) string { return env[k] })
	}
	for _, usd := range []string{"100.01", "101", "1000", "1e308", "1e9", "0", "-1", "NaN", "Inf", "+Inf", "-Inf"} {
		cfg, why := read(usd)
		if cfg != nil {
			t.Errorf("a daily limit of %s was accepted: Chester would run under a limit of $%v", usd, cfg.DailyUSD)
			continue
		}
		if !strings.Contains(why, "CHESTER_DAILY_USD") || !strings.Contains(why, "no more than 100") || strings.Contains(why, stubKey) {
			t.Errorf("a daily limit of %s: the reason is %q; it must name the setting and the bound", usd, why)
		}
		// The reason never repeats the value ("0" and "-1" aside, which the
		// bound's own digits happen to spell).
		if len(usd) > 2 && strings.Contains(why, usd) {
			t.Errorf("a daily limit of %s: the reason %q repeats the value", usd, why)
		}
	}
	// Control: the bound itself and everything under it still parse.
	for usd, want := range map[string]float64{"100": 100, "99.99": 99.99, "0.5": 0.5, "5": 5} {
		if cfg, why := read(usd); cfg == nil || cfg.DailyUSD != want {
			t.Errorf("a daily limit of %s was refused: %s", usd, why)
		}
	}
	// Chester off for this reason leaves the site as it was: the page says
	// so, a question is refused, and nothing else changes.
	app := activeTripApp(t)
	if cfg, _ := read("1e308"); cfg != nil {
		app.chester = newChester(cfg)
	}
	if w := send(t, app, "GET", "/healthz", "", "", nil); w.Code != http.StatusOK {
		t.Errorf("/healthz with a malformed limit: status %d", w.Code)
	}
	if w := askForm(t, app, url.Values{"preset": {"fuel"}}); w.Code != http.StatusServiceUnavailable || refusal(t, w) != chesterSaidOff {
		t.Errorf("a question under a malformed limit: status %d: %s", w.Code, w.Body.String())
	}
}

// --- 4. the limit is checked against what a question could cost ------------------------

func TestChesterRefusesAQuestionThatCouldPassTheLimit(t *testing.T) {
	stub := newAPIStub(t, quickScript(), quickScript())
	app := chesterApp(t, stub)
	cfg := app.chester.cfg
	// The worst case, stated: every round writing max_tokens at the output
	// price, and every search each round is allowed.
	//   quick: 3 x (8,000 x $20/M + 2 x $0.01) = $0.54
	//   plan:  3 x (32,000 x $20/M + 5 x $0.01) = $2.07
	if q, p := cfg.Quick.worstUSD(), cfg.Plan.worstUSD(); !near(q, 0.54) || !near(p, 2.07) {
		t.Fatalf("the worst cases are $%v and $%v, want $0.54 and $2.07", q, p)
	}
	now := time.Now().UTC()
	spend := func(usd float64) {
		t.Helper()
		if err := app.store.InsertChesterQuery(&ChesterQuery{AskedAt: utcString(now), Mode: chesterQuick,
			Question: "earlier", Model: stubModel, USD: usd, PosSource: "none"}); err != nil {
			t.Fatal(err)
		}
	}
	plan := url.Values{"mode": {"plan"}, "q": {"Where should we stay tomorrow night?"}}

	// $2.93 spent: a plan question could bring the day to exactly $5.00,
	// which is the limit and not past it. It is asked.
	spend(2.93)
	askEvents(t, askForm(t, app, plan))
	if n := len(stub.requests()); n != 1 {
		t.Fatalf("the API was asked %d time(s), want once: a question that fits is asked", n)
	}
	// That answer cost something, so the next plan question could pass the
	// limit. Under the old rule (refuse only at the limit) it would be asked
	// with $2.06 left and free to spend $2.07.
	spent, _ := app.store.ChesterSpent(now)
	if spent <= 2.93 || spent >= 2.95 {
		t.Fatalf("today's spend reads %v after one answer; the fixture is not what this test assumes", spent)
	}
	rowsBefore := len(chesterRows(t, app))
	w := askForm(t, app, plan)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("a plan question that could pass the limit: status %d: %s", w.Code, w.Body.String())
	}
	said := refusal(t, w)
	for _, want := range []string{"Chester has " + usd2(5-spent) + " left of today's limit of $5.00", "a plan question can cost up to $2.07",
		"was not asked", "A quick question, at up to $0.54, still fits.", "midnight UTC"} {
		if !strings.Contains(said, want) {
			t.Errorf("the refusal %q does not say %q", said, want)
		}
	}
	if len(stub.requests()) != 1 || len(chesterRows(t, app)) != rowsBefore {
		t.Error("a refused question reached the API or wrote a row")
	}
	// The cheaper mode still fits, as the refusal said, and is asked.
	askEvents(t, askForm(t, app, url.Values{"preset": {"fuel"}}))
	if n := len(stub.requests()); n != 2 {
		t.Fatalf("the API was asked %d time(s), want twice: the quick question fits", n)
	}
	// Past the point where a quick question fits, it is refused too, and is
	// not told that something cheaper would fit.
	spent, _ = app.store.ChesterSpent(now)
	spend(4.50 - spent)
	w = askForm(t, app, url.Values{"preset": {"fuel"}})
	if said := refusal(t, w); w.Code != http.StatusTooManyRequests || !strings.Contains(said, "$0.50 left") ||
		!strings.Contains(said, "a quick question can cost up to $0.54") || strings.Contains(said, "still fits") {
		t.Errorf("a quick question with $0.50 left: status %d: %q", w.Code, said)
	}
	if said := refusal(t, askForm(t, app, plan)); strings.Contains(said, "still fits") {
		t.Errorf("a plan question was told a quick one fits when it does not: %q", said)
	}
	if n := len(stub.requests()); n != 2 {
		t.Errorf("the API was asked %d time(s); a refused question must not reach it", n)
	}
	// A dearer model has a dearer worst case: the rule follows the setting.
	dear := chesterEnv(t, stub, map[string]string{"CHESTER_MODEL_PLAN": "claude-opus-5"})
	if got := dear.Plan.worstUSD(); !near(got, 3*(32000*25/1e6+0.05)) {
		t.Errorf("a plan question on a $25 model could cost $%v", got)
	}
	// The README states the rule and both figures.
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"$0.54", "$2.07", "could cost"} {
		if !bytes.Contains(readme, []byte(want)) {
			t.Errorf("README does not state the limit rule: %q is missing", want)
		}
	}
}

// --- 5. the model must be one the search tool is documented for ------------------------

func TestChesterModelMustSuitTheSearchTool(t *testing.T) {
	read := func(env map[string]string) (*chesterConfig, string) {
		env["ANTHROPIC_API_KEY"] = stubKey
		return chesterConfigFrom(func(k string) string { return env[k] })
	}
	for setting, model := range map[string]string{
		"CHESTER_MODEL_QUICK": "claude-haiku-5-5",
		"CHESTER_MODEL_PLAN":  "claude-fable-5-1",
	} {
		cfg, why := read(map[string]string{setting: model})
		if cfg != nil {
			t.Errorf("%s=%s was accepted; every question would fail", setting, model)
			continue
		}
		if !strings.Contains(why, setting) || !strings.Contains(why, chesterSearchTool) || !strings.Contains(why, "claude-opus-5-5") ||
			strings.Contains(why, model) || strings.Contains(why, stubKey) {
			t.Errorf("%s=%s: the reason is %q; it must name the setting, the tool and the models that do suit, and not the value", setting, model, why)
		}
	}
	if cfg, _ := read(map[string]string{"CHESTER_MODEL_QUICK": "no-such-model"}); cfg != nil {
		t.Error("a model nobody has heard of was accepted")
	}
	// Every model on the list parses in both settings and has a price of its
	// own, so the list cannot name a model the cap would price by guesswork.
	if len(chesterSearchModels) == 0 || !chesterSearchModel(chesterDefaultModel) {
		t.Fatal("the list is empty or does not hold the default model")
	}
	for _, model := range chesterSearchModels {
		if cfg, why := read(map[string]string{"CHESTER_MODEL_QUICK": model, "CHESTER_MODEL_PLAN": model}); cfg == nil {
			t.Errorf("%s is on the list and was refused: %s", model, why)
		}
		if _, known := chesterPriceFor(model); !known {
			t.Errorf("%s is on the list and has no price", model)
		}
	}
	// The list is for the tool version a question is really sent with.
	stub := newAPIStub(t, quickScript())
	app := chesterApp(t, stub)
	askEvents(t, askForm(t, app, url.Values{"preset": {"fuel"}}))
	if got := stub.request(0)["tools"].([]any)[0].(map[string]any)["type"]; got != chesterSearchTool {
		t.Errorf("a question is sent with the search tool %v; the list is for %s", got, chesterSearchTool)
	}
}

// --- 6. the cached prefix does not move with the car -------------------------------------

// rawField is one top-level field of a request body, byte for byte as sent.
func rawField(t *testing.T, body []byte, name string) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("the request is not JSON: %v", err)
	}
	if len(m[name]) == 0 {
		t.Fatalf("the request has no %s", name)
	}
	return m[name]
}

// Two questions from two towns in two time zones are sent with the same
// tools and the same system blocks, byte for byte: everything up to the
// cache breakpoint. Where the car is goes after it, in the user message.
func TestChesterCachedPrefixDoesNotMoveWithTheCar(t *testing.T) {
	// The fixture's checkpoints are not named as towns. Two of them are here,
	// so that the town is really in play.
	doc := fixtureTrip(t)
	for old, named := range map[string]string{
		`"name": "Birch Gate", "label": "Waypoint"`: `"name": "Birch Gate, ZZ", "label": "Waypoint"`,
		`"name": "Dogwood", "label": "Night 2"`:     `"name": "Dogwood, ZY", "label": "Night 2"`,
	} {
		if !bytes.Contains(doc, []byte(old)) {
			t.Fatalf("the fixture has no %s to rename; this test would pass on nothing", old)
		}
		doc = bytes.Replace(doc, []byte(old), []byte(named), 1)
	}
	app := tokenApp(t, testToken)
	if w := pushTrip(t, app, doc, "?activate=1"); w.Code != http.StatusOK {
		t.Fatalf("push: status %d: %s", w.Code, w.Body.String())
	}
	stub := newAPIStub(t, quickScript(), quickScript())
	app.chester = newChester(chesterEnv(t, stub, nil))

	// Birch Gate is in one zone, Dogwood in another, two days' drive apart.
	_, east := askQ(t, app, url.Values{"preset": {"fuel"}, "lat": {"41.0"}, "lng": {"-101.0"}})
	_, west := askQ(t, app, url.Values{"preset": {"fuel"}, "lat": {"43.0"}, "lng": {"-104.0"}})
	if east.TZ != "America/Chicago" || west.TZ != "America/Denver" {
		t.Fatalf("the two questions were asked in %s and %s; they must be in different zones for this test to mean anything", east.TZ, west.TZ)
	}
	reqs := stub.requests()
	if len(reqs) != 2 {
		t.Fatalf("the API was asked %d time(s), want twice", len(reqs))
	}
	a, b := reqs[0].Body, reqs[1].Body

	// The prompt renders tools, then system, then messages. The breakpoint is
	// on the last system block, so tools and system are the cached prefix.
	for _, field := range []string{"tools", "system", "model", "thinking", "output_config", "max_tokens"} {
		if x, y := rawField(t, a, field), rawField(t, b, field); !bytes.Equal(x, y) {
			t.Errorf("%s differs between the two questions, so the cached prefix is not reused:\n %s\n %s", field, x, y)
		}
	}
	var system []map[string]any
	if err := json.Unmarshal(rawField(t, a, "system"), &system); err != nil || len(system) != 2 {
		t.Fatalf("the system blocks: %v", err)
	}
	if got := js(system[len(system)-1]["cache_control"]); got != `{"type":"ephemeral"}` {
		t.Errorf("the last system block's cache_control is %s; the prefix compared above is not what is cached", got)
	}
	if !strings.Contains(js(system), "Birch Gate, ZZ") || !strings.Contains(js(system), "Dogwood, ZY") {
		t.Fatal("the plan block does not carry the renamed checkpoints; the trip pushed is not the one asked about")
	}
	// The search tool still carries a location, and it is the day's zone
	// alone: no town, no region, no number.
	var tools []map[string]any
	if err := json.Unmarshal(rawField(t, a, "tools"), &tools); err != nil || len(tools) != 3 {
		t.Fatalf("the tools: %v", err)
	}
	if loc := js(tools[0]["user_location"]); loc != `{"timezone":"America/Chicago","type":"approximate"}` {
		t.Errorf("the search location is %s, want the ride-day's zone and nothing else", loc)
	}
	// What moved with the car is in the user message, after the breakpoint.
	ua, ub := string(rawField(t, a, "messages")), string(rawField(t, b, "messages"))
	if ua == ub {
		t.Fatal("the two questions sent the same user message; the positions did not reach the request")
	}
	for body, want := range map[string][]string{
		ua: {"NEAREST TOWN ON THE PLAN: Birch Gate, ZZ.", "(America/Chicago)", "POSITION: 41.000, -101.000"},
		ub: {"NEAREST TOWN ON THE PLAN: Dogwood, ZY.", "(America/Denver)", "POSITION: 43.000, -104.000"},
	} {
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("a user message does not carry %q: %s", w, body)
			}
		}
	}
	for _, moving := range []string{"NEAREST TOWN", "America/Denver\"", "POSITION", "41.000", "43.000"} {
		if bytes.Contains(rawField(t, b, "tools"), []byte(moving)) {
			t.Errorf("the tools carry %q, which moves with the car", moving)
		}
	}
	if east.PromptVersion != "chester-rt-5" {
		t.Errorf("the prompt version is %q; the tool and the context block changed, so it must have too", east.PromptVersion)
	}
}

// --- 7. a partial answer is never an answer ----------------------------------------------

func TestChesterAPartialAnswerIsNeverAnAnswer(t *testing.T) {
	stub := newAPIStub(t,
		stream(events(evStart(stubModel, 80, 0, 0), evText(0, "The pass is PARTIAL-MARK "),
			evEnd("refusal", 5, 0, map[string]any{"type": "refusal", "category": nil, "explanation": nil}))...),
		stream(events(evStart(stubModel, 80, 0, 0), evText(0, "Here are HALF-MARK places."),
			evPlaces(1, "toolu_cut", `{"places": [{"name": "Half a pl`), evEnd("max_tokens", 8000, 0, nil))...),
		quickScript())
	app := chesterApp(t, stub)

	for _, c := range []struct{ said, partial string }{
		{chesterSaidRefusal, "The pass is PARTIAL-MARK"},
		{chesterSaidCutOff, "Here are HALF-MARK places."},
	} {
		evs, r := askQ(t, app, url.Values{"q": {"Is the pass open?"}})
		// The words were streamed as they came, and the error that follows
		// them tells the page to take them down.
		if !strings.Contains(eventText(evs), c.partial) {
			t.Fatalf("the partial %q was not streamed; this test is about nothing", c.partial)
		}
		said, _ := eventOf(evs, "error")
		if said.D != c.said || !said.Drop {
			t.Errorf("the page was told %+v, want %q and to drop the partial", said, c.said)
		}
		last := -1
		for i, e := range evs {
			if e.T == "text" {
				last = i
			}
		}
		if errAt := indexOfEvent(evs, "error"); errAt < last {
			t.Errorf("the error came at event %d, before the last of the text at %d", errAt, last)
		}
		if r.Answer != "" || r.Partial != c.partial || r.Error != c.said || r.USDEstimated {
			t.Errorf("the row: answer %q, partial %q, error %q, estimate %v", r.Answer, r.Partial, r.Error, r.USDEstimated)
		}
	}
	// Control: a whole answer is an answer, with nothing to drop.
	evs, whole := askQ(t, app, url.Values{"preset": {"fuel"}})
	if _, bad := eventOf(evs, "error"); bad || whole.Answer == "" || whole.Partial != "" {
		t.Errorf("a whole answer: answer %q, partial %q", whole.Answer, whole.Partial)
	}

	// The summary counts the two as questions that ended with no answer.
	sum := summarizeChester(chesterRows(t, app))
	if sum.N != 3 || sum.Failed != 2 || len(sum.Rows) != 1 || sum.Rows[0].N != 3 || sum.Rows[0].Failed != 2 {
		t.Errorf("the summary: %d question(s), %d failed, rows %+v", sum.N, sum.Failed, sum.Rows)
	}
	// The log page never shows a partial where an answer goes.
	page := send(t, app, "GET", "/driver/chester/log", testToken, "", nil).Body.String()
	if n := strings.Count(page, `class="ch-answer ch-answer-log"`); n != 1 {
		t.Errorf("the log page shows %d answer(s), want 1: a partial is not one", n)
	}
	for _, want := range []string{"2 ended with no answer", "3 question(s), 2 with no answer", "Not an answer: what had been written when it stopped",
		"PARTIAL-MARK", "HALF-MARK"} {
		if !strings.Contains(page, want) {
			t.Errorf("the log page lacks %q", want)
		}
	}
	for _, mark := range []string{"PARTIAL-MARK", "HALF-MARK"} {
		at := strings.Index(page, mark)
		open, closed := strings.LastIndex(page[:at], "<details"), strings.LastIndex(page[:at], "</details>")
		if open < 0 || closed > open {
			t.Errorf("the log page shows %s outside the block that says it is not an answer", mark)
		}
	}
	// The export keeps the two apart.
	export := send(t, app, "GET", "/driver/chester/log.json", testToken, "", nil).Body.String()
	if strings.Contains(export, `"answer": "The pass is`) || !strings.Contains(export, `"partial": "The pass is PARTIAL-MARK"`) ||
		!strings.Contains(export, `"failed": 2`) {
		t.Error("the export gives a partial as an answer, or does not carry it as a partial")
	}

	// The page's script takes the streamed words down when it is told to.
	script, err := staticFS.ReadFile("static/js/chester.js")
	if err != nil {
		t.Fatal(err)
	}
	_, after, found := strings.Cut(string(script), "if (ev.drop) {")
	block, _, closed := strings.Cut(after, "show(error, ev.d);")
	if !found || !closed {
		t.Fatal("the page's script does not act on an error that drops the partial")
	}
	for _, want := range []string{"answer.textContent = ''", "hide(answer)", "spoken = ''"} {
		if !strings.Contains(block, want) {
			t.Errorf("on a dropped answer the script does not run %s before it shows the sentence", want)
		}
	}
}

func indexOfEvent(evs []chesterEvent, kind string) int {
	for i, e := range evs {
		if e.T == kind {
			return i
		}
	}
	return -1
}

// --- 8. a signed-in write must come from this site ---------------------------------------

// signedInCookie signs in the way the sign-in page does and hands back the
// cookie a browser would then carry.
func signedInCookie(t *testing.T, app *App) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	app.auth.Issue(rec, false, testAdminUser(t, app))
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatal("signing in set no session cookie")
	return nil
}

type originCase struct {
	name   string
	cookie bool
	token  string
	origin string
	want   int
}

func originRequest(app *App, c originCase, cookie *http.Cookie, method, target string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.cookie {
		req.AddCookie(cookie)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, req)
	return w
}

// A write that rides on the session cookie is refused when its Origin header
// names another host, and is otherwise as it was: no Origin header, the same
// origin, and the push token are all unchanged.
func TestSignedInWritesMustComeFromThisSite(t *testing.T) {
	// httptest sends requests to example.com, so that is this site's host.
	const own, sibling = "http://example.com", "https://evil.example.com"
	cases := []originCase{
		{"the cookie, from this site's own page", true, "", own, http.StatusOK},
		{"the cookie, with no Origin header", true, "", "", http.StatusOK},
		{"the cookie, from a sibling host", true, "", sibling, http.StatusForbidden},
		{"the cookie, from the same name on another port", true, "", "http://example.com:8081", http.StatusForbidden},
		{"the cookie, from a page with no origin of its own", true, "", "null", http.StatusForbidden},
		{"the cookie, from another site altogether", true, "", "https://example.org", http.StatusForbidden},
		{"the push token, whatever the Origin", false, testToken, sibling, http.StatusOK},
		{"the push token beside the cookie", true, testToken, sibling, http.StatusOK},
		{"no credential, from this site", false, "", own, http.StatusUnauthorized},
		{"no credential, from a sibling host", false, "", sibling, http.StatusUnauthorized},
		{"a wrong token, from a sibling host", false, "ffffffffffffffffffffffff", sibling, http.StatusUnauthorized},
	}
	allowed := 0
	for _, c := range cases {
		if c.want == http.StatusOK {
			allowed++
		}
	}
	script := make([]http.HandlerFunc, allowed)
	for i := range script {
		script[i] = quickScript()
	}
	stub := newAPIStub(t, script...)
	app := chesterApp(t, stub)
	cookie := signedInCookie(t, app)
	logs := captureLog(t)

	// A question: it spends money.
	asked := 0
	for _, c := range cases {
		w := originRequest(app, c, cookie, "POST", "/chester/ask", url.Values{"preset": {"fuel"}})
		if w.Code != c.want {
			t.Errorf("a question with %s: status %d, want %d: %s", c.name, w.Code, c.want, w.Body.String())
		}
		if c.want == http.StatusOK {
			asked++
		}
		if n := len(stub.requests()); n != asked {
			t.Fatalf("after a question with %s the API has been asked %d time(s), want %d", c.name, n, asked)
		}
	}
	if n := len(chesterRows(t, app)); n != allowed {
		t.Errorf("%d row(s) in the log, want %d: a refused question writes none", n, allowed)
	}
	if !strings.Contains(logs.String(), "refused: a signed-in POST /chester/ask came from another origin") {
		t.Errorf("the app's log does not say a write was refused for its origin: %s", logs.String())
	}
	if strings.Contains(logs.String(), "evil.example.com") {
		t.Error("the app's log repeats the origin a stranger sent")
	}

	// A verdict: it writes to the log.
	id := logRow(t, app, chesterQuick, 1000, 0.01)
	target := "/driver/chester/log/" + strconv.FormatInt(id, 10)
	for _, c := range cases {
		verdict := "right"
		if c.want != http.StatusOK {
			verdict = "wrong"
		}
		w := originRequest(app, c, cookie, "POST", target, url.Values{"verdict": {verdict}})
		if w.Code != c.want {
			t.Errorf("a verdict with %s: status %d, want %d", c.name, w.Code, c.want)
		}
		if r, _ := app.store.ChesterQuery(id); r.Verdict == "wrong" {
			t.Fatalf("a verdict with %s was refused with %d and saved all the same", c.name, w.Code)
		}
	}
	if r, _ := app.store.ChesterQuery(id); r.Verdict != "right" {
		t.Fatalf("the verdicts that were let in did not save: %q; the refusals above prove nothing", r.Verdict)
	}

	// Every other signed-in write goes through the same gate.
	foreign := originCase{cookie: true, origin: sibling}
	for _, target := range []string{"/driver/lodging", "/driver/ride/active", "/driver/plan", "/driver/post/1/delete"} {
		if w := originRequest(app, foreign, cookie, "POST", target, url.Values{}); w.Code != http.StatusForbidden {
			t.Errorf("POST %s with the cookie from a sibling host: status %d, want 403", target, w.Code)
		}
	}
	// Reading is not writing: a signed-in GET is unchanged whatever its Origin.
	if w := originRequest(app, foreign, cookie, "GET", "/driver/chester/log.json", url.Values{}); w.Code != http.StatusOK {
		t.Errorf("a signed-in GET from a sibling host: status %d, want 200", w.Code)
	}

	// Behind a proxy the host a request arrives with may not be the public
	// name. SITE_URL's host is this site too, and no other host is.
	app.siteURL = "https://trip.example.net"
	reached := 0
	gate := app.requireAuth(func(http.ResponseWriter, *http.Request) { reached++ })
	for origin, want := range map[string]int{
		"https://trip.example.net":   http.StatusOK,
		"https://TRIP.example.net":   http.StatusOK,
		"http://10.0.0.7:8080":       http.StatusOK, // the host the request was sent to
		"https://other.example.net":  http.StatusForbidden,
		"https://trip.example.net.x": http.StatusForbidden,
		"trip.example.net":           http.StatusForbidden, // not an origin at all
	} {
		req := httptest.NewRequest("POST", "http://10.0.0.7:8080/chester/ask", nil)
		req.AddCookie(cookie)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		before := reached
		gate.ServeHTTP(w, req)
		if w.Code != want || (reached > before) != (want == http.StatusOK) {
			t.Errorf("behind a proxy, Origin %s: status %d, want %d", origin, w.Code, want)
		}
	}
}
