package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Chester's tests never reach the real API and never spend anything. The API
// is stood in for by apiStub, an httptest server that speaks the Messages
// API's own shapes at the SDK's HTTP boundary: a streamed answer, a web
// search result block, a tool call, a refusal, an error. The key is an
// invented string that is not shaped like a key.

const stubKey = "stub-key-for-tests-only"

// --- the stub --------------------------------------------------------------------

type stubRequest struct {
	Body   []byte
	Header http.Header
}

type apiStub struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []stubRequest
	script []http.HandlerFunc
}

// newAPIStub answers each request with the next handler in the script. A
// request past the end of the script is a test failure and a plain 400, which
// the SDK does not retry.
func newAPIStub(t *testing.T, script ...http.HandlerFunc) *apiStub {
	t.Helper()
	s := &apiStub{t: t, script: script}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.reqs = append(s.reqs, stubRequest{Body: body, Header: r.Header.Clone()})
		var next http.HandlerFunc
		if len(s.script) > 0 {
			next, s.script = s.script[0], s.script[1:]
		}
		s.mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Errorf("the stub was asked for %s %s; Chester only ever posts to /v1/messages", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if next == nil {
			t.Errorf("the stub was asked for more than its script holds")
			apiError(http.StatusBadRequest, "invalid_request_error", "no more scripted answers")(w, r)
			return
		}
		next(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *apiStub) requests() []stubRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]stubRequest(nil), s.reqs...)
}

// request is the n-th request body, decoded.
func (s *apiStub) request(n int) map[string]any {
	s.t.Helper()
	reqs := s.requests()
	if n >= len(reqs) {
		s.t.Fatalf("the stub saw %d request(s); there is no request %d", len(reqs), n)
	}
	var m map[string]any
	if err := json.Unmarshal(reqs[n].Body, &m); err != nil {
		s.t.Fatalf("request %d is not JSON: %v", n, err)
	}
	return m
}

// stream answers with server-sent events, one per JSON document given, each
// named by its own "type" as the API names them.
func stream(events ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, ev := range events {
			var head struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(ev), &head); err != nil || head.Type == "" {
				panic("the test scripted an event that is not JSON with a type: " + ev)
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", head.Type, ev)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

func apiError(status int, kind, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "error",
			"error": map[string]any{"type": kind, "message": message}})
	}
}

func js(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// The pieces of a streamed message, in the API's shapes.

func evStart(model string, in, cacheWrite, cacheRead int) string {
	return js(map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_stub", "type": "message", "role": "assistant", "model": model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": in, "output_tokens": 1,
			"cache_creation_input_tokens": cacheWrite, "cache_read_input_tokens": cacheRead}}})
}

func evBlock(idx int, block map[string]any, deltas ...map[string]any) []string {
	out := []string{js(map[string]any{"type": "content_block_start", "index": idx, "content_block": block})}
	for _, d := range deltas {
		out = append(out, js(map[string]any{"type": "content_block_delta", "index": idx, "delta": d}))
	}
	return append(out, js(map[string]any{"type": "content_block_stop", "index": idx}))
}

func evText(idx int, chunks ...string) []string {
	var deltas []map[string]any
	for _, c := range chunks {
		deltas = append(deltas, map[string]any{"type": "text_delta", "text": c})
	}
	return evBlock(idx, map[string]any{"type": "text", "text": ""}, deltas...)
}

func evThinking(idx int) []string {
	return evBlock(idx, map[string]any{"type": "thinking", "thinking": "", "signature": ""},
		map[string]any{"type": "signature_delta", "signature": "c3R1Yg=="})
}

// evSearch is one web search: the server's tool call and its result block.
func evSearch(idx int, id string) []string {
	out := evBlock(idx, map[string]any{"type": "server_tool_use", "id": id, "name": "web_search", "input": map[string]any{}},
		map[string]any{"type": "input_json_delta", "partial_json": `{"query": "fuel near Birch Gate"}`})
	return append(out, evBlock(idx+1, map[string]any{"type": "web_search_tool_result", "tool_use_id": id,
		"content": []any{map[string]any{"type": "web_search_result", "url": "https://example.com/fuel",
			"title": "An invented fuel stop", "encrypted_content": "c3R1Yg==", "page_age": nil}}})...)
}

// evSearchError is a web search that failed: HTTP 200, and an error object
// where the result list would be.
func evSearchError(idx int, id, code string) []string {
	out := evBlock(idx, map[string]any{"type": "server_tool_use", "id": id, "name": "web_search", "input": map[string]any{}},
		map[string]any{"type": "input_json_delta", "partial_json": `{"query": "x"}`})
	return append(out, evBlock(idx+1, map[string]any{"type": "web_search_tool_result", "tool_use_id": id,
		"content": map[string]any{"type": "web_search_tool_result_error", "error_code": code}})...)
}

// evPlaces is the show_places call, its input streamed in pieces the way the
// API streams it.
func evPlaces(idx int, id, input string) []string {
	var deltas []map[string]any
	for len(input) > 0 {
		n := min(17, len(input))
		deltas = append(deltas, map[string]any{"type": "input_json_delta", "partial_json": input[:n]})
		input = input[n:]
	}
	return evBlock(idx, map[string]any{"type": "tool_use", "id": id, "name": chesterToolName, "input": map[string]any{}}, deltas...)
}

func evEnd(stop string, out, searches int, details map[string]any) []string {
	usage := map[string]any{"output_tokens": out}
	if searches > 0 {
		usage["server_tool_use"] = map[string]any{"web_search_requests": searches, "web_fetch_requests": 0}
	}
	return []string{
		js(map[string]any{"type": "message_delta", "usage": usage,
			"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil, "stop_details": details}}),
		js(map[string]any{"type": "message_stop"}),
	}
}

func events(parts ...any) []string {
	var out []string
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			out = append(out, v)
		case []string:
			out = append(out, v...)
		default:
			panic("events takes strings and string slices")
		}
	}
	return out
}

const stubModel = "claude-opus-5-5"

// placesInput is what the model sends to show_places: one lodging, one fuel
// stop with markup in its name, and a web address that is not one.
const placesInput = `{"places": [` +
	`{"name": "Juniper Motel", "kind": "lodging", "town": "Cedar Camp, ZZ", "why": "Invented, and near the road.", "url": "https://example.com/juniper", "night": ""},` +
	`{"name": "Pump <b>& Go</b>", "kind": "fuel", "town": "Birch Gate, ZZ", "why": "Open late, they say.", "url": "javascript:alert(1)", "night": ""}` +
	`]}`

// --- the app ------------------------------------------------------------------------

// chesterEnv builds settings the way boot does, from an environment that
// holds only what the test put there.
func chesterEnv(t *testing.T, stub *apiStub, extra map[string]string) *chesterConfig {
	t.Helper()
	env := map[string]string{"ANTHROPIC_API_KEY": stubKey, "ANTHROPIC_BASE_URL": stub.srv.URL}
	for k, v := range extra {
		env[k] = v
	}
	cfg, why := chesterConfigFrom(func(k string) string { return env[k] })
	if cfg == nil {
		t.Fatalf("the settings did not parse: %s", why)
	}
	return cfg
}

// chesterApp is the fixture trip with Chester switched on and pointed at the stub.
func chesterApp(t *testing.T, stub *apiStub) *App {
	t.Helper()
	app := activeTripApp(t)
	app.chester = newChester(chesterEnv(t, stub, nil))
	return app
}

func askForm(t *testing.T, app *App, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return postForm(t, app, "/chester/ask", form)
}

// askEvents reads a streamed answer back into its events.
func askEvents(t *testing.T, w *httptest.ResponseRecorder) []chesterEvent {
	t.Helper()
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("not a streamed answer: status %d, %s: %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	var out []chesterEvent
	sc := bufio.NewScanner(bytes.NewReader(w.Body.Bytes()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ev chesterEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("a line of the answer is not JSON: %q", sc.Text())
		}
		out = append(out, ev)
	}
	return out
}

func eventText(evs []chesterEvent) string {
	var b strings.Builder
	for _, e := range evs {
		if e.T == "text" {
			b.WriteString(e.D)
		}
	}
	return b.String()
}

func eventOf(evs []chesterEvent, kind string) (chesterEvent, bool) {
	for _, e := range evs {
		if e.T == kind {
			return e, true
		}
	}
	return chesterEvent{}, false
}

// refusal reads the sentence a question was refused with before it was sent.
func refusal(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct{ Status, Error string }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Status != "refused" {
		t.Fatalf("not a refusal: status %d: %s", w.Code, w.Body.String())
	}
	return body.Error
}

func chesterRows(t *testing.T, app *App) []ChesterQuery {
	t.Helper()
	rows, err := app.store.ChesterQueries(0)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	return rows
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// --- A. access --------------------------------------------------------------------------

// Every Chester route refuses a visitor who is not signed in, and the API is
// never asked.
func TestChesterRefusesAnonymous(t *testing.T) {
	stub := newAPIStub(t)
	app := chesterApp(t, stub)
	if err := app.store.InsertChesterQuery(&ChesterQuery{AskedAt: utcString(time.Now()), Mode: chesterQuick,
		Question: "LOG-MARKER question", Model: stubModel, Answer: "LOG-MARKER answer", PosSource: "none"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method, target string
		want           int
	}{
		{"GET", "/chester", http.StatusFound},
		{"POST", "/chester/ask", http.StatusUnauthorized},
		{"GET", "/driver/chester/log", http.StatusFound},
		{"GET", "/driver/chester/log.json", http.StatusFound},
		{"POST", "/driver/chester/log/1", http.StatusUnauthorized},
	} {
		for _, token := range []string{"", "0123456789abcdef0123456X"} {
			w := send(t, app, c.method, c.target, token, "application/x-www-form-urlencoded",
				[]byte("preset=fuel&verdict=right"))
			if w.Code != c.want {
				t.Errorf("%s %s with token %q: status %d, want %d", c.method, c.target, token, w.Code, c.want)
			}
			if c.want == http.StatusFound && !strings.HasPrefix(w.Header().Get("Location"), "/driver/login") {
				t.Errorf("%s %s: sent to %q, want the sign-in page", c.method, c.target, w.Header().Get("Location"))
			}
			if strings.Contains(w.Body.String(), "LOG-MARKER") {
				t.Errorf("%s %s: an anonymous answer carries the log", c.method, c.target)
			}
		}
	}
	if n := len(stub.requests()); n != 0 {
		t.Errorf("the API was asked %d time(s) for anonymous visitors", n)
	}
	if rows := chesterRows(t, app); len(rows) != 1 || rows[0].Verdict != "" {
		t.Errorf("an anonymous request changed the log: %d row(s), verdict %q", len(rows), rows[0].Verdict)
	}
	// Positive control: the same requests with the token are let in.
	if w := send(t, app, "GET", "/driver/chester/log.json", testToken, "", nil); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), "LOG-MARKER") {
		t.Fatalf("the token does not open the log: status %d", w.Code)
	}
	// No open page or endpoint carries any of the log, signed in or not.
	for _, p := range []string{"/", "/plan", "/plan.json", "/photos", "/route.geojson", "/checklist", "/healthz", "/robots.txt"} {
		if body := send(t, app, "GET", p, "", "", nil).Body.String(); strings.Contains(body, "LOG-MARKER") {
			t.Errorf("%s carries the log", p)
		}
	}
	// The masthead link is for the signed-in trip-keeper only.
	if strings.Contains(send(t, app, "GET", "/", "", "", nil).Body.String(), `href="/chester"`) {
		t.Error("an anonymous page links to Chester")
	}
	if !strings.Contains(send(t, app, "GET", "/", testToken, "", nil).Body.String(), `href="/chester"`) {
		t.Error("the signed-in masthead has no link to Chester while it is on")
	}
}

// With no key Chester is absent: the settings say why, the page says so, the
// masthead has no link, and a question is refused before anything is sent.
func TestChesterDisabledWithoutAKey(t *testing.T) {
	cfg, why := chesterConfigFrom(func(string) string { return "" })
	if cfg != nil || why != "ANTHROPIC_API_KEY is not set" {
		t.Fatalf("no key: config %v, reason %q", cfg != nil, why)
	}
	// A setting that does not parse leaves it off too, and the reason names
	// the setting and never a value.
	for name, env := range map[string]map[string]string{
		"CHESTER_EFFORT_QUICK": {"CHESTER_EFFORT_QUICK": "extreme"},
		"CHESTER_EFFORT_PLAN":  {"CHESTER_EFFORT_PLAN": "none"},
		"CHESTER_DAILY_USD":    {"CHESTER_DAILY_USD": "plenty"},
		"CHESTER_FALLBACK":     {"CHESTER_FALLBACK": "sometimes"},
		"ANTHROPIC_BASE_URL":   {"ANTHROPIC_BASE_URL": "not a url"},
	} {
		env["ANTHROPIC_API_KEY"] = stubKey
		cfg, why := chesterConfigFrom(func(k string) string { return env[k] })
		if cfg != nil || !strings.Contains(why, name) || strings.Contains(why, stubKey) {
			t.Errorf("%s: config %v, reason %q", name, cfg != nil, why)
		}
	}
	for _, usd := range []string{"0", "-1", "NaN", "Inf"} {
		env := map[string]string{"ANTHROPIC_API_KEY": stubKey, "CHESTER_DAILY_USD": usd}
		if cfg, _ := chesterConfigFrom(func(k string) string { return env[k] }); cfg != nil {
			t.Errorf("a daily limit of %s was accepted", usd)
		}
	}

	app := activeTripApp(t)
	page := send(t, app, "GET", "/chester", testToken, "", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Chester is not set up") {
		t.Errorf("the page with no key: status %d, and it does not say Chester is not set up", page.Code)
	}
	for _, absent := range []string{`id="ch-form"`, "chester.js", `id="ch-send"`} {
		if strings.Contains(page.Body.String(), absent) {
			t.Errorf("the page with no key still carries %s", absent)
		}
	}
	if strings.Contains(send(t, app, "GET", "/", testToken, "", nil).Body.String(), `href="/chester"`) {
		t.Error("the masthead links to Chester while it is off")
	}
	w := askForm(t, app, url.Values{"preset": {"fuel"}})
	if w.Code != http.StatusServiceUnavailable || refusal(t, w) != chesterSaidOff {
		t.Errorf("a question with no key: status %d: %s", w.Code, w.Body.String())
	}
	if rows := chesterRows(t, app); len(rows) != 0 {
		t.Errorf("a refused question wrote %d row(s)", len(rows))
	}
}

// The defaults, and the settings that override them.
func TestChesterSettings(t *testing.T) {
	stub := newAPIStub(t)
	cfg := chesterEnv(t, stub, nil)
	if cfg.Quick.Model != "claude-opus-5-5" || cfg.Plan.Model != "claude-opus-5-5" ||
		cfg.Quick.Effort != "low" || cfg.Plan.Effort != "medium" || cfg.DailyUSD != 5 || !cfg.Fallback {
		t.Errorf("defaults: %+v / %+v, $%v, fallback %v", cfg.Quick, cfg.Plan, cfg.DailyUSD, cfg.Fallback)
	}
	if cfg.Quick.MaxUses != 2 || cfg.Plan.MaxUses != 5 || cfg.Quick.Timeout != 60*time.Second || cfg.Plan.Timeout != 180*time.Second {
		t.Errorf("per-mode limits: quick %+v, plan %+v", cfg.Quick, cfg.Plan)
	}
	cfg = chesterEnv(t, stub, map[string]string{"CHESTER_MODEL_QUICK": "claude-sonnet-5-5", "CHESTER_MODEL_PLAN": "claude-opus-5",
		"CHESTER_EFFORT_QUICK": "MEDIUM", "CHESTER_EFFORT_PLAN": "xhigh", "CHESTER_DAILY_USD": "1.50", "CHESTER_FALLBACK": "off"})
	if cfg.Quick.Model != "claude-sonnet-5-5" || cfg.Plan.Model != "claude-opus-5" ||
		cfg.Quick.Effort != "medium" || cfg.Plan.Effort != "xhigh" || cfg.DailyUSD != 1.5 || cfg.Fallback {
		t.Errorf("overrides: %+v / %+v, $%v, fallback %v", cfg.Quick, cfg.Plan, cfg.DailyUSD, cfg.Fallback)
	}
}

// --- B. a quick question, end to end ---------------------------------------------------------

func quickScript() http.HandlerFunc {
	return stream(events(
		evStart(stubModel, 200, 900, 0),
		evThinking(0),
		evText(1, "Fuel is about ", "20 miles ahead at Birch Gate. ", "That is an estimate."),
		evPlaces(2, "toolu_stub", placesInput),
		evEnd("tool_use", 50, 0, nil),
	)...)
}

func TestChesterQuickQuestionEndToEnd(t *testing.T) {
	stub := newAPIStub(t, quickScript())
	app := chesterApp(t, stub)
	logs := captureLog(t)

	w := askForm(t, app, url.Values{"preset": {"fuel"}, "lat": {"41.0001"}, "lng": {"-101.0002"}})
	evs := askEvents(t, w)

	// What was sent: one request, in the shape the settings promise.
	reqs := stub.requests()
	if len(reqs) != 1 {
		t.Fatalf("the API was asked %d time(s), want once: the cards came with the answer", len(reqs))
	}
	// This SDK release sends an API key as a bearer credential.
	if got := reqs[0].Header.Get("Authorization"); got != "Bearer "+stubKey {
		t.Errorf("the request carried the key %q, want the configured one", got)
	}
	if got := reqs[0].Header.Get("Anthropic-Beta"); got != "server-side-fallback-2026-07-01" {
		t.Errorf("anthropic-beta is %q, want the fallback opt-in", got)
	}
	if got := reqs[0].Header.Get("Traceparent"); got != "" {
		t.Errorf("the request carried a trace header %q", got)
	}
	req := stub.request(0)
	if req["model"] != "claude-opus-5-5" || req["stream"] != true || req["fallbacks"] != "default" {
		t.Errorf("model %v, stream %v, fallbacks %v", req["model"], req["stream"], req["fallbacks"])
	}
	if got := js(req["thinking"]); got != `{"type":"adaptive"}` {
		t.Errorf("thinking is %s, want adaptive and nothing else", got)
	}
	if got := js(req["output_config"]); got != `{"effort":"low"}` {
		t.Errorf("output_config is %s, want low effort", got)
	}
	if _, forced := req["tool_choice"]; forced {
		t.Errorf("tool_choice was sent: %s", js(req["tool_choice"]))
	}
	for _, banned := range []string{"temperature", "top_p", "top_k", "budget_tokens"} {
		if bytes.Contains(reqs[0].Body, []byte(`"`+banned+`"`)) {
			t.Errorf("the request carries %s", banned)
		}
	}
	tools, _ := req["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("%d tools, want the web search and the place cards", len(tools))
	}
	search, places := tools[0].(map[string]any), tools[1].(map[string]any)
	if search["type"] != "web_search_20260209" || search["name"] != "web_search" || search["max_uses"] != float64(2) {
		t.Errorf("the search tool: %s", js(search))
	}
	if loc := js(search["user_location"]); strings.Contains(loc, "41.0") || strings.Contains(loc, "101.0") ||
		!strings.Contains(loc, `"timezone":"America/Chicago"`) {
		t.Errorf("the search location is %s, want a zone and no coordinate", loc)
	}
	if places["name"] != chesterToolName || places["strict"] != true || places["eager_input_streaming"] != true {
		t.Errorf("the places tool: %s", js(places))
	}
	if schema := js(places["input_schema"]); strings.Count(schema, `"additionalProperties":false`) != 2 {
		t.Errorf("the places schema does not close both objects: %s", schema)
	}
	system, _ := req["system"].([]any)
	if len(system) != 2 {
		t.Fatalf("%d system blocks, want the prompt and the plan", len(system))
	}
	if _, marked := system[0].(map[string]any)["cache_control"]; marked {
		t.Error("the prompt block carries a breakpoint of its own; one on the plan covers both")
	}
	if got := js(system[1].(map[string]any)["cache_control"]); got != `{"type":"ephemeral"}` {
		t.Errorf("the plan block's cache_control is %s", got)
	}
	plan := system[1].(map[string]any)["text"].(string)
	for _, want := range []string{"Alder to Elm", "Cedar Camp", "Birch Gate", "over the ridge", "America/Denver"} {
		if !strings.Contains(plan, want) {
			t.Errorf("the plan block does not mention %q", want)
		}
	}
	messages, _ := req["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("%d messages, want one", len(messages))
	}
	user := js(messages[0])
	q, _ := chesterPreset("fuel")
	for _, want := range []string{"MODE: quick", q, "41.000, -101.000", "sent from the phone", "NOW: "} {
		if !strings.Contains(user, want) {
			t.Errorf("the user message does not carry %q: %s", want, user)
		}
	}
	// What changes by the minute is after the breakpoint, never before it.
	for _, volatile := range []string{"NOW:", "POSITION:", "QUESTION:", "41.000"} {
		if strings.Contains(js(system), volatile) {
			t.Errorf("the cached part of the prompt carries %q", volatile)
		}
	}

	// What came back: the words in order, the cards, the end.
	const answer = "Fuel is about 20 miles ahead at Birch Gate. That is an estimate."
	if got := eventText(evs); got != answer {
		t.Errorf("the streamed text is %q", got)
	}
	if first := evs[0]; first.T != "status" || first.D != "Thinking" {
		t.Errorf("the first event is %+v, want the thinking status", first)
	}
	if _, bad := eventOf(evs, "error"); bad {
		t.Errorf("a good answer carried an error event: %+v", evs)
	}
	cards, ok := eventOf(evs, "places")
	if !ok || len(cards.Places) != 2 {
		t.Fatalf("the cards: %+v", cards)
	}
	motel, pump := cards.Places[0], cards.Places[1]
	if motel.Name != "Juniper Motel" || motel.KindLabel != "Lodging" || motel.Town != "Cedar Camp, ZZ" || motel.Night == "" {
		t.Errorf("the lodging card: %+v", motel)
	}
	night, _ := time.Parse(dateLayout, motel.Night)
	wantLinks := []chesterLink{
		{"Navigate", "https://waze.com/ul?navigate=yes&q=Juniper+Motel%2C+Cedar+Camp%2C+ZZ"},
		{"Map", "https://www.google.com/maps/search/?api=1&query=Juniper+Motel%2C+Cedar+Camp%2C+ZZ"},
		{"Booking search", "https://www.travelocity.com/Hotel-Search?destination=Cedar+Camp%2C+ZZ&endDate=" +
			night.AddDate(0, 0, 1).Format(dateLayout) + "&startDate=" + motel.Night},
		{"Website", "https://example.com/juniper"},
	}
	if js(motel.Links) != js(wantLinks) {
		t.Errorf("the lodging card's links:\n got %s\nwant %s", js(motel.Links), js(wantLinks))
	}
	// The model's text reaches a link only as an escaped query, and a web
	// address that is not one is dropped, not repaired.
	if pump.Name != "Pump <b>& Go</b>" || pump.URL != "" || len(pump.Links) != 2 || pump.Night != "" {
		t.Errorf("the fuel card: %+v", pump)
	}
	for _, l := range pump.Links {
		if u, err := url.Parse(l.Href); err != nil || u.Scheme != "https" || strings.ContainsAny(l.Href, "<>\" ") ||
			(u.Host != "waze.com" && u.Host != "www.google.com") {
			t.Errorf("the fuel card links to %q", l.Href)
		}
	}
	if strings.Contains(w.Body.String(), "javascript:") {
		t.Error("the model's javascript: address reached the page")
	}
	done, ok := eventOf(evs, "done")
	if !ok || evs[len(evs)-1].T != "done" || done.Stop != "tool_use" || done.ID == 0 {
		t.Fatalf("the last event: %+v", evs[len(evs)-1])
	}

	// What it cost, from the response's own counts: 200 in, 900 written to
	// the cache, 50 out, at $4, $5 and $20 a million.
	const wantUSD = (200*4 + 900*5 + 50*20) / 1e6
	if !near(done.USD, wantUSD) {
		t.Errorf("the cost is %v, want %v", done.USD, wantUSD)
	}

	// The row.
	rows := chesterRows(t, app)
	if len(rows) != 1 {
		t.Fatalf("%d row(s) in the log, want 1", len(rows))
	}
	r := rows[0]
	if r.ID != done.ID || r.Ride != fixtureSlug || r.Mode != chesterQuick || r.Preset != "fuel" || r.Question != q ||
		r.Model != "claude-opus-5-5" || r.ServedModel != "" || r.Effort != "low" || r.PromptVersion != chesterPromptVersion {
		t.Errorf("the row's identity: %+v", r)
	}
	if r.Answer != answer || r.StopReason != "tool_use" || r.Error != "" || len(r.Places) != 2 {
		t.Errorf("the row's answer: %q, stop %q, error %q, %d place(s)", r.Answer, r.StopReason, r.Error, len(r.Places))
	}
	if r.TokensIn != 200 || r.TokensOut != 50 || r.CacheWrite != 900 || r.CacheRead != 0 || r.Searches != 0 || !near(r.USD, wantUSD) {
		t.Errorf("the row's counts: in %d out %d written %d read %d searches %d $%v",
			r.TokensIn, r.TokensOut, r.CacheWrite, r.CacheRead, r.Searches, r.USD)
	}
	if r.FirstMS == nil || *r.FirstMS <= 0 || r.TotalMS < *r.FirstMS {
		t.Errorf("the row's times: first word %v ms, total %d ms", r.FirstMS, r.TotalMS)
	}
	if r.PosLat == nil || *r.PosLat != 41 || *r.PosLng != -101 || r.PosSource != "browser" || r.TZ != "America/Chicago" {
		t.Errorf("the row's position: %s in %s", r.Position(), r.TZ)
	}
	if at, ok := parseUTC(r.AskedAt); !ok || time.Since(at) > time.Minute {
		t.Errorf("the row's time: %q", r.AskedAt)
	}

	// The key is in the request header and nowhere else.
	for name, text := range map[string]string{"the answer": w.Body.String(), "the app's log": logs.String(), "the row": js(r),
		"the page": send(t, app, "GET", "/chester", testToken, "", nil).Body.String(),
		"the log":  send(t, app, "GET", "/driver/chester/log.json", testToken, "", nil).Body.String()} {
		if strings.Contains(text, stubKey) {
			t.Errorf("%s carries the key", name)
		}
	}
	// And the app's log says what happened without saying what was asked.
	if !strings.Contains(logs.String(), "chester: a quick question ended (tool_use)") {
		t.Errorf("the app's log has no line for the question: %s", logs.String())
	}
	for _, private := range []string{q, "Juniper", "Birch Gate"} {
		if strings.Contains(logs.String(), private) {
			t.Errorf("the app's log carries %q", private)
		}
	}
}

// The log page renders a model's place name as text, and builds its links itself.
func TestChesterLogPageRendersCardsAsText(t *testing.T) {
	stub := newAPIStub(t, quickScript())
	app := chesterApp(t, stub)
	askEvents(t, askForm(t, app, url.Values{"preset": {"fuel"}}))
	page := send(t, app, "GET", "/driver/chester/log", testToken, "", nil)
	body := page.Body.String()
	if page.Code != http.StatusOK {
		t.Fatalf("log page: status %d", page.Code)
	}
	for _, want := range []string{"Juniper Motel", "Pump &lt;b&gt;&amp; Go&lt;/b&gt;", "Booking search", "Navigate",
		`href="https://waze.com/ul?navigate=yes&amp;q=Juniper&#43;Motel%2C&#43;Cedar&#43;Camp%2C&#43;ZZ"`,
		"claude-opus-5-5", "tool_use", "Not ruled", `id="q-1"`, `action="/driver/chester/log/1"`,
		"Right", "Partly", "Wrong", "Unverified", "plus web searches at $10 per 1,000"} {
		if !strings.Contains(body, want) {
			t.Errorf("the log page lacks %q", want)
		}
	}
	for _, banned := range []string{"<b>& Go</b>", "javascript:alert", stubKey} {
		if strings.Contains(body, banned) {
			t.Errorf("the log page carries %q", banned)
		}
	}
	if got := page.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("the log page may be cached: %q", got)
	}
}

// --- C. a plan question, with searching ----------------------------------------------------------

func TestChesterPlanQuestion(t *testing.T) {
	stub := newAPIStub(t, stream(events(
		evStart(stubModel, 1000, 0, 900),
		evText(0, "Let me look."),
		evSearch(1, "srvtoolu_a"),
		evSearch(3, "srvtoolu_b"),
		evText(5, "Two real options for tomorrow night", ", and I would take the first."),
		evPlaces(6, "toolu_plan", `{"places": [{"name": "Larch Lodge", "kind": "lodging", "town": "Dogwood, ZZ", "why": "Invented.", "url": "", "night": "2030-05-02"}]}`),
		evEnd("tool_use", 400, 2, nil),
	)...))
	app := chesterApp(t, stub)
	evs := askEvents(t, askForm(t, app, url.Values{"mode": {"plan"}, "q": {"Where should we stay tomorrow night?"}}))

	req := stub.request(0)
	if got := js(req["output_config"]); got != `{"effort":"medium"}` {
		t.Errorf("plan effort: %s", got)
	}
	if search := req["tools"].([]any)[0].(map[string]any); search["max_uses"] != float64(5) {
		t.Errorf("plan searches: %s", js(search))
	}
	if req["max_tokens"] != float64(32000) || !strings.Contains(js(req["messages"]), "MODE: plan") {
		t.Errorf("plan request: max_tokens %v", req["max_tokens"])
	}
	// The words either side of the searches are two paragraphs, not one run.
	const answer = "Let me look.\n\nTwo real options for tomorrow night, and I would take the first."
	if got := eventText(evs); got != answer {
		t.Errorf("the streamed text is %q", got)
	}
	searching := 0
	for _, e := range evs {
		if e.T == "status" && e.D == "Searching the web" {
			searching++
		}
	}
	// Two searches back to back, with nothing said between them, are one
	// status line: the page is told once.
	if searching != 1 {
		t.Errorf("%d searching status line(s), want one for two searches in a row", searching)
	}
	cards, _ := eventOf(evs, "places")
	if len(cards.Places) != 1 || cards.Places[0].Night != "2030-05-02" ||
		!strings.Contains(cards.Places[0].Links[2].Href, "startDate=2030-05-02") ||
		!strings.Contains(cards.Places[0].Links[2].Href, "endDate=2030-05-03") {
		t.Errorf("the lodging card for a named night: %+v", cards.Places)
	}
	// 1000 in and 400 out at $4 and $20, 900 read from the cache at $0.20,
	// and two searches at a cent each.
	const wantUSD = (1000*4+400*20)/1e6 + 900*0.20/1e6 + 2*0.01
	r := chesterRows(t, app)[0]
	if r.Mode != chesterPlan || r.Effort != "medium" || r.Searches != 2 || r.CacheRead != 900 || !near(r.USD, wantUSD) {
		t.Errorf("the plan row: mode %s effort %s searches %d cache read %d $%v, want $%v",
			r.Mode, r.Effort, r.Searches, r.CacheRead, r.USD, wantUSD)
	}
	if r.Answer != answer || r.PosSource != "none" || r.PosLat != nil || r.Preset != "" {
		t.Errorf("the plan row: %q, position %s", r.Answer, r.Position())
	}
}

// The cards came before a word was written: one more request lets the answer
// be written, with the model's own turn sent back unchanged.
func TestChesterAsksAgainWhenTheCardsCameFirst(t *testing.T) {
	stub := newAPIStub(t,
		stream(events(evStart(stubModel, 100, 0, 0), evThinking(0), evPlaces(1, "toolu_first", placesInput), evEnd("tool_use", 30, 0, nil))...),
		stream(events(evStart(stubModel, 150, 0, 0), evText(0, "Both are on your way."), evEnd("end_turn", 10, 0, nil))...))
	app := chesterApp(t, stub)
	evs := askEvents(t, askForm(t, app, url.Values{"preset": {"food"}}))
	if len(stub.requests()) != 2 {
		t.Fatalf("the API was asked %d time(s), want twice", len(stub.requests()))
	}
	second := stub.request(1)
	msgs := second["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("the second request has %d messages, want the question, the model's turn and the tool result", len(msgs))
	}
	turn := js(msgs[1])
	for _, want := range []string{`"role":"assistant"`, `"type":"thinking"`, `"signature":"c3R1Yg=="`, `"type":"tool_use"`, "Juniper Motel"} {
		if !strings.Contains(turn, want) {
			t.Errorf("the model's turn went back without %s: %s", want, turn)
		}
	}
	if result := js(msgs[2]); !strings.Contains(result, `"tool_use_id":"toolu_first"`) || !strings.Contains(result, `"type":"tool_result"`) {
		t.Errorf("the tool result: %s", result)
	}
	// The tool is answered with the request for the words, not with a bare
	// acknowledgement the model can take as the end of its turn.
	if result := js(msgs[2]); !strings.Contains(result, "Nothing you wrote before this call was shown") || !strings.Contains(result, "Now write the whole answer") {
		t.Errorf("the tool result does not ask for the words: %s", result)
	}
	// The prefix of the second request is the first request's, byte for byte.
	first := stub.request(0)
	if js(first["system"]) != js(second["system"]) || js(first["tools"]) != js(second["tools"]) ||
		js(first["messages"].([]any)[0]) != js(msgs[0]) {
		t.Error("the second request edited what the first one sent")
	}
	r := chesterRows(t, app)[0]
	if eventText(evs) != "Both are on your way." || len(r.Places) != 2 || r.StopReason != "end_turn" ||
		r.TokensIn != 250 || r.TokensOut != 40 {
		t.Errorf("the row: %q, %d place(s), stop %s, %d in, %d out", r.Answer, len(r.Places), r.StopReason, r.TokensIn, r.TokensOut)
	}
}

// A paused turn is resumed by sending the model's own words back, with
// nothing added.
func TestChesterResumesAPausedTurn(t *testing.T) {
	stub := newAPIStub(t,
		stream(events(evStart(stubModel, 100, 0, 0), evText(0, "Still looking. "), evSearch(1, "srvtoolu_p"), evEnd("pause_turn", 20, 1, nil))...),
		stream(events(evStart(stubModel, 300, 0, 0), evText(0, "Found it."), evEnd("end_turn", 10, 0, nil))...))
	app := chesterApp(t, stub)
	evs := askEvents(t, askForm(t, app, url.Values{"preset": {"stop"}}))
	msgs := stub.request(1)["messages"].([]any)
	if len(msgs) != 2 || !strings.Contains(js(msgs[1]), `"role":"assistant"`) || !strings.Contains(js(msgs[1]), "server_tool_use") {
		t.Fatalf("the resumed request's messages: %s", js(msgs))
	}
	r := chesterRows(t, app)[0]
	// What was written after the pause is a new paragraph of the same answer.
	if eventText(evs) != "Still looking. \n\nFound it." || r.Answer != "Still looking. \n\nFound it." || r.Searches != 1 || r.Error != "" {
		t.Errorf("the resumed answer: %q, %d search(es), error %q", eventText(evs), r.Searches, r.Error)
	}
}

// --- D. the limits ---------------------------------------------------------------------------------

func TestChesterDailyCapRefusesInPlainWords(t *testing.T) {
	stub := newAPIStub(t, quickScript())
	app := chesterApp(t, stub)
	now := time.Now().UTC()
	spend := func(at time.Time, usd float64) {
		t.Helper()
		if err := app.store.InsertChesterQuery(&ChesterQuery{AskedAt: utcString(at), Mode: chesterQuick,
			Question: "earlier", Model: stubModel, USD: usd, PosSource: "none"}); err != nil {
			t.Fatal(err)
		}
	}
	// Yesterday's spending is yesterday's: it does not count.
	spend(now.AddDate(0, 0, -1), 50)
	spend(now, 4.40)
	if got, _ := app.store.ChesterSpent(now); !near(got, 4.40) {
		t.Fatalf("today's spend reads %v, want 4.40", got)
	}
	// Room for the most a quick question could cost: answered.
	askEvents(t, askForm(t, app, url.Values{"preset": {"fuel"}}))
	spent, _ := app.store.ChesterSpent(now)
	spend(now, 5-spent)
	// At it: refused, in words, and the API is not asked.
	w := askForm(t, app, url.Values{"preset": {"fuel"}})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: status %d: %s", w.Code, w.Body.String())
	}
	said := refusal(t, w)
	if !strings.Contains(said, "today's limit") || !strings.Contains(said, "of $5.00") || !strings.Contains(said, "midnight UTC") {
		t.Errorf("the refusal reads %q", said)
	}
	if n := len(stub.requests()); n != 1 {
		t.Errorf("the API was asked %d time(s); the refused question must not reach it", n)
	}
	if n := len(chesterRows(t, app)); n != 4 {
		t.Errorf("%d rows, want 4: a refused question writes none", n)
	}
	// The page says where the day stands.
	if page := send(t, app, "GET", "/chester", testToken, "", nil).Body.String(); !strings.Contains(page, "of $5.00 used") {
		t.Error("the page does not show the day's limit")
	}
}

// A model the price table does not know is priced as the dearest column of
// any it does, so the cap can only be reached sooner.
func TestChesterCost(t *testing.T) {
	u := chesterUsage{In: 1_000_000, Out: 1_000_000, CacheWrite: 1_000_000, CacheRead: 1_000_000, Searches: 3}
	for model, want := range map[string]float64{
		"claude-opus-5-5":   4 + 20 + 5 + 0.20 + 0.03,
		"claude-sonnet-5-5": 2 + 10 + 2.50 + 0.20 + 0.03,
		"claude-fable-5-1":  10 + 50 + 12.50 + 0.25 + 0.03,
		"some-future-model": 10 + 50 + 12.50 + 1.00 + 0.03,
	} {
		if got := chesterCost(model, "", u); !near(got, want) {
			t.Errorf("%s costs %v, want %v", model, got, want)
		}
	}
	if _, known := chesterPriceFor("some-future-model"); known {
		t.Error("an unknown model was reported as known")
	}
	for model := range chesterPrices {
		if p, _ := chesterPriceFor(model); chesterCost("some-future-model", "", u) < chesterCost(model, "", u) || p.In <= 0 {
			t.Errorf("%s is dearer than an unknown model, or has no price", model)
		}
	}
	// A fallback is priced at the dearer of the model asked and the one that answered.
	if got, want := chesterCost("claude-opus-5-5", "claude-opus-4-8", chesterUsage{In: 1e6, Out: 1e6}), 5.0+25.0; !near(got, want) {
		t.Errorf("a fallback costs %v, want %v", got, want)
	}
	if got := chesterCost("claude-opus-5-5", "claude-opus-5-5", chesterUsage{In: 1e6}); !near(got, 4) {
		t.Errorf("the same model twice costs %v", got)
	}
}

// One question at a time: the second is told so at once, and is not sent.
func TestChesterBusyRefusal(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	slow := func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		quickScript()(w, r)
	}
	stub := newAPIStub(t, slow)
	app := chesterApp(t, stub)
	first := make(chan *httptest.ResponseRecorder)
	go func() { first <- askForm(t, app, url.Values{"preset": {"fuel"}}) }()
	<-entered
	w := askForm(t, app, url.Values{"preset": {"coffee"}})
	if w.Code != http.StatusConflict || refusal(t, w) != chesterSaidBusy {
		t.Errorf("a second question while one is running: status %d: %s", w.Code, w.Body.String())
	}
	close(release)
	if evs := askEvents(t, <-first); eventText(evs) == "" {
		t.Error("the first question lost its answer")
	}
	if n := len(stub.requests()); n != 1 {
		t.Errorf("the API was asked %d time(s), want 1", n)
	}
	// And once it has finished, the next one would be taken.
	if !app.chesterBusy.TryLock() {
		t.Fatal("Chester is still busy after the question finished")
	}
	app.chesterBusy.Unlock()
}

func TestChesterRefusesWhatItCannotAsk(t *testing.T) {
	stub := newAPIStub(t)
	app := chesterApp(t, stub)
	for name, c := range map[string]struct {
		form url.Values
		want string
	}{
		"nothing asked":     {url.Values{}, chesterSaidEmpty},
		"only spaces":       {url.Values{"q": {"   "}}, chesterSaidEmpty},
		"an unknown mode":   {url.Values{"q": {"hello"}, "mode": {"deep"}}, chesterSaidMode},
		"an unknown preset": {url.Values{"preset": {"teleport"}}, chesterSaidPreset},
		"too long":          {url.Values{"q": {strings.Repeat("a", maxChesterQuestion+1)}}, chesterSaidLong},
	} {
		w := askForm(t, app, c.form)
		if w.Code != http.StatusBadRequest || refusal(t, w) != c.want {
			t.Errorf("%s: status %d: %s", name, w.Code, w.Body.String())
		}
	}
	if len(stub.requests()) != 0 || len(chesterRows(t, app)) != 0 {
		t.Error("a question that could not be asked reached the API or the log")
	}
}

// --- E. when it goes wrong -----------------------------------------------------------------------------

// Every way a question can end badly is one plain sentence on the page, a
// row in the log with the question in it, and never a half answer passed off
// as a whole one.
func TestChesterFailuresAreSaidPlainly(t *testing.T) {
	const question = "Is the pass open?"
	cases := []struct {
		name     string
		script   http.HandlerFunc
		wantSaid string
		wantStop string
		wantNote string
		partial  string // what had been written; kept, and never the answer
	}{
		{"a refusal before any word",
			stream(events(evStart(stubModel, 80, 0, 0),
				evEnd("refusal", 1, 0, map[string]any{"type": "refusal", "category": "cyber", "explanation": "declined"}))...),
			chesterSaidRefusal, "refusal", "ended: refusal", ""},
		{"a refusal part way through",
			stream(events(evStart(stubModel, 80, 0, 0), evText(0, "The pass is "),
				evEnd("refusal", 5, 0, map[string]any{"type": "refusal", "category": nil, "explanation": nil}))...),
			chesterSaidRefusal, "refusal", "ended: refusal", "The pass is"},
		{"cut off at the length limit, mid tool call",
			stream(events(evStart(stubModel, 80, 0, 0), evText(0, "Here are some places."),
				evPlaces(1, "toolu_cut", `{"places": [{"name": "Half a pl`), evEnd("max_tokens", 8000, 0, nil))...),
			chesterSaidCutOff, "max_tokens", "ended: max_tokens", "Here are some places."},
		{"the API will not take the request",
			apiError(http.StatusBadRequest, "invalid_request_error", "tools.1.custom: something it did not like"),
			"The API would not take the question: tools.1.custom: something it did not like", "", "ended: api_400", ""},
		{"the key is refused",
			apiError(http.StatusUnauthorized, "authentication_error", "invalid x-api-key"),
			"The API refused the key Chester was given. It needs setting again.", "", "ended: api_401", ""},
		{"the model is unknown",
			apiError(http.StatusNotFound, "not_found_error", "model: no-such-model"),
			"The API does not know the model Chester is set to use.", "", "ended: api_404", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub := newAPIStub(t, c.script)
			app := chesterApp(t, stub)
			evs := askEvents(t, askForm(t, app, url.Values{"q": {question}}))
			said, ok := eventOf(evs, "error")
			if !ok || said.D != c.wantSaid {
				t.Errorf("the page was told %+v, want %q", said, c.wantSaid)
			}
			if last := evs[len(evs)-1]; last.T != "done" || last.ID == 0 {
				t.Errorf("the answer did not end with done and a row id: %+v", last)
			}
			if _, cards := eventOf(evs, "places"); cards {
				t.Error("a failed question showed place cards")
			}
			rows := chesterRows(t, app)
			if len(rows) != 1 {
				t.Fatalf("%d row(s), want 1: a failed question is still a row", len(rows))
			}
			r := rows[0]
			if r.Question != question || r.Error != c.wantSaid || r.StopReason != c.wantStop || !strings.Contains(r.Note, c.wantNote) ||
				r.Answer != "" || r.Partial != c.partial || len(r.Places) != 0 {
				t.Errorf("the row: question %q, error %q, stop %q, note %q, answer %q, partial %q, %d place(s)",
					r.Question, r.Error, r.StopReason, r.Note, r.Answer, r.Partial, len(r.Places))
			}
			if len(stub.requests()) != 1 {
				t.Errorf("the API was asked %d time(s); these failures are not retried", len(stub.requests()))
			}
			page := send(t, app, "GET", "/driver/chester/log", testToken, "", nil).Body.String()
			if !strings.Contains(page, question) {
				t.Error("the log page does not show the failed question")
			}
		})
	}
	// The page's script never empties the box: on a failure the question is
	// still there to send again.
	script, err := staticFS.ReadFile("static/js/chester.js")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(script, []byte("box.value")) {
		t.Fatal("the script does not touch the question box at all; this check would pass on nothing")
	}
	for _, clears := range []string{"box.value = ''", `box.value = ""`, "box.value=''", "form.reset("} {
		if bytes.Contains(script, []byte(clears)) {
			t.Errorf("the page's script clears the question box (%s)", clears)
		}
	}
	if bytes.Contains(script, []byte("innerHTML")) {
		t.Error("the page's script writes markup; an answer is text")
	}
}

// A web search that failed arrives as a block, not as an error: the answer
// stands and the row says a search failed.
func TestChesterNotesAFailedSearch(t *testing.T) {
	stub := newAPIStub(t, stream(events(evStart(stubModel, 90, 0, 0), evSearchError(0, "srvtoolu_x", "max_uses_exceeded"),
		evText(2, "I could not confirm this by searching."), evEnd("end_turn", 12, 1, nil))...))
	app := chesterApp(t, stub)
	evs := askEvents(t, askForm(t, app, url.Values{"preset": {"weather"}}))
	if _, bad := eventOf(evs, "error"); bad {
		t.Error("a failed search was shown as a failed question")
	}
	r := chesterRows(t, app)[0]
	if r.Answer != "I could not confirm this by searching." || r.Note != "a web search failed: max_uses_exceeded" || r.Searches != 1 {
		t.Errorf("the row: %q, note %q, %d search(es)", r.Answer, r.Note, r.Searches)
	}
}

// A question that outlives its limit is stopped and says so.
func TestChesterTimeout(t *testing.T) {
	hung := func(w http.ResponseWriter, r *http.Request) {
		stream(events(evStart(stubModel, 70, 0, 0))...)(w, r)
		<-r.Context().Done()
	}
	stub := newAPIStub(t, hung)
	app := chesterApp(t, stub)
	app.chester.cfg.Quick.Timeout = 150 * time.Millisecond
	start := time.Now()
	evs := askEvents(t, askForm(t, app, url.Values{"preset": {"howfar"}}))
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("a 150 ms limit took %s to stop the question", took)
	}
	said, _ := eventOf(evs, "error")
	if !strings.Contains(said.D, "took longer than") || !strings.Contains(said.D, "was stopped") {
		t.Errorf("the page was told %q", said.D)
	}
	r := chesterRows(t, app)[0]
	if !strings.Contains(r.Note, "ended: timeout") || r.TokensIn != 70 || r.USD <= 0 {
		t.Errorf("the row: note %q, %d tokens in, $%v: what was spent before the stop still counts", r.Note, r.TokensIn, r.USD)
	}
}

// --- F. what Chester is told ----------------------------------------------------------------------------

const overlayMark = "OVERLAY-CHESTER"

// The request that leaves the machine carries the plan as the public pages
// show it and one position that has been through the store's fuzz seam. It
// carries no private overlay text, no checklist private text, and no raw
// coordinate of a private place, however the position reached it.
func TestChesterIsToldNothingPrivate(t *testing.T) {
	stub := newAPIStub(t, quickScript(), quickScript(), quickScript())
	app := checklistApp(t)
	app.chester = newChester(chesterEnv(t, stub, nil))

	// The private things, all really present.
	if w := postForm(t, app, "/driver/lodging", url.Values{"address-1": {overlayMark + " 1 Invented Lane"},
		"notes-1": {overlayMark + " gate code"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("save the overlay: status %d", w.Code)
	}
	markOK(t, app, clStep, "inspect", url.Values{"outcome": {"done"}, "private_note": {privateNoteMark + " kept"}})
	// A post pinned at the raw finish, as an exact pin with the fuzz off.
	legs := mustLegs(t, app, fixtureSlug)
	rawLat, rawLng := 44.3217, -106.2789
	post := &Post{LegID: legs[len(legs)-1].ID, Kind: "note", OccurredAt: utcString(time.Now()), TZ: "America/Denver",
		Title: "at the door", Lat: &rawLat, Lng: &rawLng, PinSource: pinBrowser}
	if err := app.store.CreatePost(post); err != nil {
		t.Fatal(err)
	}
	// Positive controls: every one of them is on the volume or in the database.
	onDisk, _ := os.ReadFile(tripPath(app.dataDir, fixtureSlug))
	for _, raw := range fixtureRaw {
		if !bytes.Contains(onDisk, []byte(raw)) {
			t.Fatalf("the fixture on the volume does not contain %s; this test would pass on nothing", raw)
		}
	}
	if overlay, _ := os.ReadFile(app.dataDir + "/private.json"); !bytes.Contains(overlay, []byte(overlayMark)) {
		t.Fatal("the overlay on the volume does not hold the marker; this test would pass on nothing")
	}
	if cl := js(census(t, app, fixtureSlug)); !strings.Contains(cl, privateMark) || !strings.Contains(cl, privateNoteMark) {
		t.Fatal("the checklist holds no private text; this test would pass on nothing")
	}
	if p, err := app.store.Post(post.ID); err != nil || p.Lat == nil || *p.Lat != rawLat {
		t.Fatal("the post is not pinned at the raw private point; this test would pass on nothing")
	}

	asks := []struct {
		name  string
		form  url.Values
		point string // the published point the request must carry instead
		label string
	}{
		// The phone, standing on the raw start.
		{"the phone at the private place", url.Values{"preset": {"fuel"}, "lat": {"40.1234"}, "lng": {"-100.4321"}},
			"40.100, -100.450", "near Alder"},
		// The phone a few kilometres off, outside the place's own cell.
		{"the phone just outside its cell", url.Values{"preset": {"fuel"}, "lat": {"40.1534"}, "lng": {"-100.4321"}},
			"40.100, -100.450", "near Alder"},
		// No phone position: the last pinned post, which sits on the raw finish.
		{"the last pinned post", url.Values{"preset": {"fuel"}}, "44.300, -106.300", "near Elm"},
	}
	for i, ask := range asks {
		askEvents(t, askForm(t, app, ask.form))
		body := string(stub.requests()[i].Body)
		// The request is the real one: it holds the plan.
		for _, want := range []string{"Alder", "Elm", "Cedar Camp", "POSITION: "} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s: the request does not carry %q; it is not the request this test is about", ask.name, want)
			}
		}
		// And the point it gives is the published one, said to be approximate.
		for _, want := range []string{ask.point, "private place"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: the request does not carry %q", ask.name, want)
			}
		}
		for _, raw := range append(append([]string{}, fixtureRaw...), "40.123", "100.432", "44.321", "44.322", "106.278", "106.279", "40.153") {
			if strings.Contains(body, raw) {
				t.Errorf("%s: the request carries the raw private number %s", ask.name, raw)
			}
		}
		for _, private := range []string{overlayMark, privateMark, privateNoteMark, "Invented Lane", "gate code", "at the door"} {
			if strings.Contains(body, private) {
				t.Errorf("%s: the request carries private text %q", ask.name, private)
			}
		}
		// The search hint is a zone and nothing finer.
		if loc := js(stub.request(i)["tools"].([]any)[0].(map[string]any)["user_location"]); strings.ContainsAny(loc, "0123456789") {
			t.Errorf("%s: the search location carries a number: %s", ask.name, loc)
		}
	}
	// The log holds the published points too.
	rows := chesterRows(t, app)
	if len(rows) != len(asks) {
		t.Fatalf("%d rows, want %d", len(rows), len(asks))
	}
	for i, ask := range asks {
		r := rows[len(rows)-1-i]
		if r.PosLat == nil || !strings.HasPrefix(r.Position(), ask.point) || r.PosLabel != ask.label {
			t.Errorf("%s: the row's position is %q", ask.name, r.Position())
		}
	}
	export := send(t, app, "GET", "/driver/chester/log.json", testToken, "", nil).Body.String()
	for _, raw := range fixtureRaw {
		if strings.Contains(export, raw) {
			t.Errorf("the log export carries the raw private number %s", raw)
		}
	}
}

// The position seam on its own: a private place's cell, the wider mask
// around it, and the open road.
func TestPublishPosition(t *testing.T) {
	app := activeTripApp(t)
	cps := rideCheckpoints(mustLegs(t, app, fixtureSlug))
	for _, c := range []struct {
		name     string
		lat, lng float64
		want     LatLng
		private  string
	}{
		{"on the private start", 40.1234, -100.4321, fixtureStart, "Alder"},
		{"in its cell", 40.11, -100.46, fixtureStart, "Alder"},
		{"outside its cell, inside the mask", 40.1534, -100.4321, fixtureStart, "Alder"},
		{"on the private finish", 44.3217, -106.2789, fixtureFinish, "Elm"},
		{"on the open road", 41.00049, -101.00051, LatLng{Lat: 41, Lng: -101.001}, ""},
		{"well clear of the start", 40.3, -100.7, LatLng{Lat: 40.3, Lng: -100.7}, ""},
	} {
		got, pc := publishPosition(c.lat, c.lng, cps)
		name := ""
		if pc != nil {
			name = pc.Name
		}
		if got != c.want || name != c.private {
			t.Errorf("%s: published %+v (private place %q), want %+v (%q)", c.name, got, name, c.want, c.private)
		}
	}
}

// On a driving day Chester is told the day, tonight's stop and the miles left.
func TestChesterContextOnADrivingDay(t *testing.T) {
	app := activeTripApp(t)
	// Noon on day 1, where day 1 starts.
	now := time.Date(2030, 5, 1, 12, 0, 0, 0, loadLoc("America/Chicago"))
	// Birch Gate is a vertex of the day's line, part way along it.
	lat, lng := 41.0, -101.0
	cx, err := app.chesterContext(now, &lat, &lng)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NOW: Wed 1 May 2030, 12:00 CDT (America/Chicago).", "TODAY: day 1 of 3, Alder → Cedar Camp.",
		"TONIGHT: Cedar Camp.", "MILES LEFT TODAY: about ", " of 600.0, measured along the planned line.",
		"POSITION: 41.000, -101.000, sent from the phone just now."} {
		if !strings.Contains(cx.Volatile, want) {
			t.Errorf("the context does not say %q:\n%s", want, cx.Volatile)
		}
	}
	// Somewhere between none and all of the day is left.
	var left float64
	if _, after, ok := strings.Cut(cx.Volatile, "MILES LEFT TODAY: about "); ok {
		left, _ = strconv.ParseFloat(strings.Fields(after)[0], 64)
	}
	if left <= 50 || left >= 550 {
		t.Errorf("%v miles left from the middle of a 600 mile day", left)
	}
	if cx.TZ != "America/Chicago" || cx.Tonight != "2030-05-01" || cx.Ride != fixtureSlug {
		t.Errorf("zone %s, tonight %s, ride %s", cx.TZ, cx.Tonight, cx.Ride)
	}
	// Far from the day's line there is no figure to give, and it says so.
	lat, lng = 30, -90
	cx, _ = app.chesterContext(now, &lat, &lng)
	if !strings.Contains(cx.Volatile, "MILES LEFT TODAY: unknown: the position is not near today's planned line.") {
		t.Errorf("far from the line:\n%s", cx.Volatile)
	}
	// With no position at all, it says that too.
	cx, _ = app.chesterContext(now, nil, nil)
	if cx.Pos != nil || !strings.Contains(cx.Volatile, "POSITION: unknown.") || !strings.Contains(cx.Volatile, "MILES LEFT TODAY: unknown without a position.") {
		t.Errorf("with no position:\n%s", cx.Volatile)
	}
	// The plan block is the same from one minute to the next, so it can be cached.
	later, _ := app.chesterContext(now.Add(7*time.Minute), &lat, &lng)
	if later.Stable != cx.Stable || later.Volatile == cx.Volatile {
		t.Error("the plan block changed with the clock, or the volatile block did not")
	}
}

func TestCoarseTown(t *testing.T) {
	for name, want := range map[string][2]string{
		"Bend, OR": {"Bend", "OR"}, "Moose Jaw, SK": {"Moose Jaw", "SK"},
		"Cedar Camp": {"", ""}, "12 Invented Lane, Bend, OR": {"", ""}, "Somewhere, Oregon": {"", ""}, ", OR": {"", ""},
	} {
		if city, region := coarseTown(name); city != want[0] || region != want[1] {
			t.Errorf("%q: %q, %q", name, city, region)
		}
	}
}

// --- G. the place list is read strictly ---------------------------------------------------------------------

func TestParsePlaces(t *testing.T) {
	ps, err := parsePlaces(placesInput, 3, "2030-05-01")
	if err != nil || len(ps) != 2 || ps[0].Night != "2030-05-01" || ps[1].URL != "" {
		t.Fatalf("the fixture input: %+v, %v", ps, err)
	}
	for name, raw := range map[string]string{
		"not JSON":           `{"places": [{"name": "Half a pl`,
		"an empty object":    `{}`,
		"another shape":      `{"spots": []}`,
		"an unknown field":   `{"places": [{"name": "A", "kind": "fuel", "town": "", "why": "", "url": "", "night": "", "html": "<b>"}]}`,
		"a second document":  `{"places": []} {"places": []}`,
		"a list, not a form": `[{"name": "A"}]`,
	} {
		if ps, err := parsePlaces(raw, 3, ""); err == nil {
			t.Errorf("%s was read as %+v", name, ps)
		}
	}
	ps, err = parsePlaces(`{"places": [`+
		`{"name": "  ", "kind": "fuel", "town": "", "why": "", "url": "", "night": ""},`+
		`{"name": "One\nTwo\u0000", "kind": "spaceport", "town": "T", "why": "`+strings.Repeat("w", 400)+`", "url": "ftp://x/y", "night": "soon"},`+
		`{"name": "B", "kind": "food", "town": "", "why": "", "url": "https://user:pw@example.com/", "night": "2030-05-02"},`+
		`{"name": "C", "kind": "food", "town": "", "why": "", "url": "", "night": ""},`+
		`{"name": "D", "kind": "food", "town": "", "why": "", "url": "", "night": ""}]}`, 3, "2030-05-01")
	if err != nil || len(ps) != 3 {
		t.Fatalf("%d place(s), %v; want the nameless one dropped and the list cut at 3", len(ps), err)
	}
	if ps[0].Name != "One Two" || ps[0].Kind != "other" || len(ps[0].Why) != 300 || ps[0].URL != "" || ps[0].Night != "" {
		t.Errorf("the cleaned card: %+v", ps[0])
	}
	if ps[1].URL != "" || ps[1].Night != "" {
		t.Errorf("an address with a password in it, or a night on a meal, was kept: %+v", ps[1])
	}
	for raw, want := range map[string]string{
		"https://example.com/a?b=c": "https://example.com/a?b=c", "http://example.com": "http://example.com",
		"javascript:alert(1)": "", "//example.com/x": "", "example.com": "", "https://": "", "data:text/html,x": "",
		"https://example.com/\"onmouseover=": "", " https://example.com ": "https://example.com",
	} {
		if got := cleanURL(raw); got != want {
			t.Errorf("cleanURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

// --- H. the verdict and the summary -------------------------------------------------------------------------

func logRow(t *testing.T, app *App, mode string, ms int64, usd float64) int64 {
	t.Helper()
	q := &ChesterQuery{AskedAt: utcString(time.Now()), TZ: "America/Chicago", Mode: mode, Question: "a question",
		Model: stubModel, Effort: "low", TotalMS: ms, USD: usd, Answer: "an answer", PosSource: "none"}
	if err := app.store.InsertChesterQuery(q); err != nil {
		t.Fatal(err)
	}
	return q.ID
}

func TestChesterVerdictIsSavedInPlace(t *testing.T) {
	app := chesterApp(t, newAPIStub(t))
	id := logRow(t, app, chesterQuick, 1200, 0.01)
	target := "/driver/chester/log/" + strconv.FormatInt(id, 10)

	// From the page: one tap on a verdict, answered by a redirect to the row.
	w := browserPost(t, app, target, url.Values{"verdict": {"partly"}, "against": {"navigation"},
		"note": {"ten minutes out"}, "chester_value": {"50"}, "reference_value": {"40"}, "unit": {"minutes"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/driver/chester/log?ok=saved#q-"+strconv.FormatInt(id, 10) {
		t.Fatalf("a verdict from the page: status %d, to %q", w.Code, w.Header().Get("Location"))
	}
	r, _ := app.store.ChesterQuery(id)
	if r.Verdict != "partly" || r.Checked != "navigation" || r.VerdictNote != "ten minutes out" ||
		r.ChesterValue == nil || *r.ChesterValue != 50 || *r.RefValue != 40 || r.Unit != "minutes" || r.VerdictAt == "" {
		t.Errorf("the saved verdict: %+v", r)
	}
	if pct, ok := r.PctError(); !ok || !near(pct, 25) {
		t.Errorf("50 against 40 is %v%% out, want 25", pct)
	}
	page := send(t, app, "GET", "/driver/chester/log?ok=saved", testToken, "", nil).Body.String()
	for _, want := range []string{"Verdict saved.", "Partly", "Navigation app", "ten minutes out", "50 / 40 minutes", `class="ch-tap ch-vb on"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the log page after a verdict lacks %q", want)
		}
	}
	// One tap with nothing else is a whole verdict, and it replaces the last.
	if w := postForm(t, app, target, url.Values{"verdict": {"right"}}); w.Code != http.StatusOK {
		t.Fatalf("a bare verdict from curl: status %d: %s", w.Code, w.Body.String())
	}
	if r, _ := app.store.ChesterQuery(id); r.Verdict != "right" || r.Checked != "" || r.ChesterValue != nil || r.Unit != "" {
		t.Errorf("after a bare verdict: %+v", r)
	}

	// What is refused, by code from the page and by sentence from curl.
	for code, form := range map[string]url.Values{
		chErrVerdict: {"verdict": {"mostly"}},
		chErrChecked: {"verdict": {"right"}, "against": {"a hunch"}},
		chErrNote:    {"verdict": {"right"}, "note": {strings.Repeat("n", maxChesterNote+1)}},
		chErrPair:    {"verdict": {"right"}, "chester_value": {"5"}},
		chErrNumber:  {"verdict": {"right"}, "chester_value": {"five"}, "reference_value": {"4"}},
		chErrUnit:    {"verdict": {"right"}, "chester_value": {"5"}, "reference_value": {"4"}, "unit": {"furlongs"}},
	} {
		w := browserPost(t, app, target, form)
		if want := "/driver/chester/log?error=" + code + "#q-" + strconv.FormatInt(id, 10); w.Code != http.StatusSeeOther || w.Header().Get("Location") != want {
			t.Errorf("%s from the page: status %d, to %q", code, w.Code, w.Header().Get("Location"))
		}
		if w := postForm(t, app, target, form); w.Code != http.StatusBadRequest || refusal(t, w) != chesterRefusals[code] {
			t.Errorf("%s from curl: status %d: %s", code, w.Code, w.Body.String())
		}
		if page := send(t, app, "GET", "/driver/chester/log?error="+code, testToken, "", nil).Body.String(); !strings.Contains(page, "Not saved: "+html.EscapeString(chesterRefusals[code])) {
			t.Errorf("the log page does not print the sentence for %s", code)
		}
	}
	if r, _ := app.store.ChesterQuery(id); r.Verdict != "right" {
		t.Errorf("a refused verdict changed the row to %q", r.Verdict)
	}
	if w := postForm(t, app, "/driver/chester/log/9999", url.Values{"verdict": {"right"}}); w.Code != http.StatusNotFound {
		t.Errorf("a verdict on a row that is not there: status %d", w.Code)
	}
	// A code the page does not know prints nothing of a stranger's choosing.
	if page := send(t, app, "GET", "/driver/chester/log?error=%3Cb%3Ehello&ok=%3Ci%3E", testToken, "", nil).Body.String(); strings.Contains(page, "hello") || strings.Contains(page, "Not saved") {
		t.Error("the log page printed an unknown code")
	}
}

func TestChesterSummaryArithmetic(t *testing.T) {
	app := chesterApp(t, newAPIStub(t))
	rule := func(id int64, form url.Values) {
		t.Helper()
		if w := postForm(t, app, "/driver/chester/log/"+strconv.FormatInt(id, 10), form); w.Code != http.StatusOK {
			t.Fatalf("verdict: %d %s", w.Code, w.Body.String())
		}
	}
	pair := func(verdict, cv, rv, unit string) url.Values {
		return url.Values{"verdict": {verdict}, "chester_value": {cv}, "reference_value": {rv}, "unit": {unit}}
	}
	// Quick, right: 1000, 3000 and 2000 ms; 110 against 100 and 45 against 50.
	rule(logRow(t, app, chesterQuick, 1000, 0.01), pair("right", "110", "100", "miles"))
	rule(logRow(t, app, chesterQuick, 3000, 0.02), pair("right", "45", "50", "minutes"))
	rule(logRow(t, app, chesterQuick, 2000, 0.03), url.Values{"verdict": {"right"}})
	// Quick, wrong: 4000 and 6000 ms; a pair against zero is not an error figure.
	rule(logRow(t, app, chesterQuick, 4000, 0.10), pair("wrong", "30", "0", "dollars"))
	rule(logRow(t, app, chesterQuick, 6000, 0.20), pair("wrong", "200", "100", "dollars"))
	// Plan, not ruled: one row.
	logRow(t, app, chesterPlan, 90000, 1.25)

	sum := summarizeChester(chesterRows(t, app))
	if sum.N != 6 || sum.Ruled != 5 || !near(sum.USD, 1.61) {
		t.Errorf("totals: %d rows, %d ruled, $%v", sum.N, sum.Ruled, sum.USD)
	}
	want := []chesterSummaryRow{
		{Mode: chesterQuick, Verdict: "right", N: 3, MedianMS: 2000, USD: 0.06, Pairs: 2, MAPE: 10},
		{Mode: chesterQuick, Verdict: "wrong", N: 2, MedianMS: 5000, USD: 0.30, Pairs: 1, MAPE: 100},
		{Mode: chesterPlan, Verdict: "", N: 1, MedianMS: 90000, USD: 1.25},
	}
	if len(sum.Rows) != len(want) {
		t.Fatalf("%d summary rows, want %d: %+v", len(sum.Rows), len(want), sum.Rows)
	}
	for i, w := range want {
		g := sum.Rows[i]
		if g.Mode != w.Mode || g.Verdict != w.Verdict || g.N != w.N || g.MedianMS != w.MedianMS ||
			!near(g.USD, w.USD) || g.Pairs != w.Pairs || !near(g.MAPE, w.MAPE) {
			t.Errorf("summary row %d: got %+v, want %+v", i, g, w)
		}
	}
	if medianMS(nil) != 0 || medianMS([]int64{7}) != 7 || medianMS([]int64{9, 1, 5, 3}) != 4 {
		t.Error("the median is wrong")
	}
	page := send(t, app, "GET", "/driver/chester/log", testToken, "", nil).Body.String()
	for _, want := range []string{"6 question(s), 5 ruled, $1.61 in all", "Quick &middot; Right", "median 2.0 s",
		"mean error 10.0% over 2 pair(s)", "Quick &middot; Wrong", "mean error 100.0% over 1 pair(s)",
		"Plan &middot; Not ruled", "median 90.0 s", "no number pairs"} {
		if !strings.Contains(page, want) {
			t.Errorf("the log page lacks %q", want)
		}
	}
}

// The export is the whole log, for the signed-in trip-keeper and the token only.
func TestChesterLogExport(t *testing.T) {
	app := chesterApp(t, newAPIStub(t))
	for i := 0; i < 3; i++ {
		logRow(t, app, chesterQuick, int64(1000*(i+1)), 0.01)
	}
	if w := send(t, app, "GET", "/driver/chester/log.json", "", "", nil); w.Code != http.StatusFound || strings.Contains(w.Body.String(), "an answer") {
		t.Fatalf("the export without a credential: status %d", w.Code)
	}
	w := send(t, app, "GET", "/driver/chester/log.json", testToken, "", nil)
	var export struct {
		PromptVersion string         `json:"prompt_version"`
		Summary       chesterSummary `json:"summary"`
		Queries       []ChesterQuery `json:"queries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &export); err != nil || w.Code != http.StatusOK {
		t.Fatalf("the export: status %d, %v", w.Code, err)
	}
	if len(export.Queries) != 3 || export.Summary.N != 3 || export.PromptVersion != chesterPromptVersion ||
		export.Queries[0].TotalMS != 3000 || export.Queries[0].Answer != "an answer" {
		t.Errorf("the export: %d queries, summary of %d, newest first %d ms", len(export.Queries), export.Summary.N, export.Queries[0].TotalMS)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("the export may be cached: %q", got)
	}
	// While the whole site is private the same routes are still closed to a
	// visitor, by the site's own gate.
	app.sitePrivate = true
	for _, p := range []string{"/chester", "/driver/chester/log", "/driver/chester/log.json"} {
		if w := send(t, app, "GET", p, "", "", nil); w.Code != http.StatusFound {
			t.Errorf("%s on a private site: status %d", p, w.Code)
		}
	}
}

// The page: the presets, the switch, the limits said once, and nothing that
// opens a dialog.
func TestChesterPageRenders(t *testing.T) {
	app := chesterApp(t, newAPIStub(t))
	w := send(t, app, "GET", "/chester", testToken, "", nil)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	for _, p := range chesterPresets {
		if !strings.Contains(body, `data-preset="`+p.ID+`"`) || !strings.Contains(body, ">"+strings.ReplaceAll(p.Label, "'", "&#39;")+"</button>") {
			t.Errorf("the page has no button for %s", p.Label)
		}
	}
	if len(chesterPresets) != 8 {
		t.Errorf("%d presets, want 8", len(chesterPresets))
	}
	for _, want := range []string{"For the passenger, or when parked.", "no live traffic", `name="mode" value="quick" checked`,
		`name="mode" value="plan"`, ">Ask</button>", "Send my position", "Read the answer aloud", `aria-live="polite"`,
		"/static/js/chester.js", "$0.00 of $5.00 used", "claude-opus-5-5 at low effort", "claude-opus-5-5 at medium effort",
		`href="/driver/chester/log"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Contains(body, "Chester is not set up") {
		t.Error("the page says Chester is not set up while it is on")
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("the page may be cached: %q", got)
	}
	script, _ := staticFS.ReadFile("static/js/chester.js")
	for _, dialog := range []string{"alert(", "confirm(", "prompt(", "showModal", "<dialog"} {
		if bytes.Contains(script, []byte(dialog)) || strings.Contains(body, dialog) {
			t.Errorf("the page or its script opens a dialog (%s)", dialog)
		}
	}
	if !bytes.Contains(script, []byte("speechSynthesis")) || !bytes.Contains(script, []byte("geolocation")) {
		t.Error("the script has no read-aloud or no position")
	}
}
