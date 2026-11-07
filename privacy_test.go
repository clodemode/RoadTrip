package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Two drivers may share the car. Each driver's Chester is private to them.

// The shared default ranking is made without any driver's memory, so it may
// be cached for everyone. A personal ranking carries memory and is never kept.
func TestSharedPickNotesCarryNoMemoryAndPersonalOnesAreNeverShared(t *testing.T) {
	tt := newTT(t)
	fillAll(tt)
	shared := `{"picks":[{"id":"coffee-1","note":"shared-note"}]}`
	personal := `{"picks":[{"id":"coffee-1","note":"PERSONAL-NOTE"}]}`
	stub := newAPIStub(t, messageJSON(shared), messageJSON(personal), messageJSON(shared))
	app, kris := picksApp(t, tt, stub)
	clockAt(app, "2026-11-07T12:00:00Z")
	_ = app.store.AddChesterLeanings("kris", []string{"kris likes dispensary stops"}, time.Now())
	_ = app.store.AddChesterTurn("kris", "where is the nearest dispensary", "KRIS-PRIVATE-ANSWER", time.Now())

	findPicks(t, app, kris, true)
	body := string(stub.requests()[0].Body)
	for _, leak := range []string{"dispensar", "KRIS-PRIVATE-ANSWER", "RECENT EXCHANGES"} {
		if strings.Contains(body, leak) {
			t.Fatalf("the shared ranking was made with kris's memory (%q)", leak)
		}
	}
	// Kris re-asks with his own line: memory is in, and the result is his alone.
	w := doAs(t, app, kris, "POST", "/driver/freelance/picks",
		url.Values{"lat": {"30.2000"}, "lng": {"-97.8000"}, "force": {"1"}, "want": {"hungry now"}})
	if w.Code != 200 || !strings.Contains(string(stub.requests()[1].Body), "dispensary stops") {
		t.Fatalf("a personal ranking uses the driver's memory: %d", w.Code)
	}
	dana := asUser(t, app, "dana", roleAdmin)
	goDestined(t, app, dana, "hudson bend")
	o := findPicks(t, app, dana, true)
	raw, _ := json.Marshal(o)
	if strings.Contains(string(raw), "PERSONAL-NOTE") || !strings.Contains(string(raw), "shared-note") {
		t.Fatalf("dana gets the shared ranking and never kris's personal one: %s", raw)
	}
	if len(stub.requests()) != 2 {
		t.Fatalf("the shared ranking is cached for dana: %d calls", len(stub.requests()))
	}
}

func logAs(t *testing.T, app *App, user, q, a string, usd float64) int64 {
	t.Helper()
	r := &ChesterQuery{User: user, AskedAt: utcString(time.Now()), TZ: "UTC", Mode: chesterQuick, Question: q, Answer: a,
		Model: stubModel, TokensIn: 111, TokensOut: 222, USD: usd, PosSource: "none"}
	if err := app.store.InsertChesterQuery(r); err != nil {
		t.Fatal(err)
	}
	return r.ID
}

func TestChesterLogShowsEachDriverOnlyTheirOwn(t *testing.T) {
	app := chesterApp(t, newAPIStub(t))
	kris := asUser(t, app, "kris", roleEditor)
	dana := asUser(t, app, "dana", roleEditor)
	boss := asUser(t, app, "boss", roleAdmin)
	krisID := logAs(t, app, "kris", "where are the cannabis dispensaries", "KRIS-ANSWER", 0.03)
	logAs(t, app, "dana", "best tacos near here", "DANA-ANSWER", 0.05)
	logAs(t, app, "", "an old question", "OLD-ANSWER", 0.07)

	for _, path := range []string{"/driver/chester/log", "/driver/chester/log.json"} {
		body := doAs(t, app, dana, "GET", path, nil).Body.String()
		for _, leak := range []string{"cannabis", "KRIS-ANSWER", "an old question", "OLD-ANSWER"} {
			if strings.Contains(body, leak) {
				t.Errorf("dana's %s shows %q", path, leak)
			}
		}
		if !strings.Contains(body, "best tacos") {
			t.Errorf("dana's %s lacks her own question", path)
		}
	}
	// An admin sees date, cost and tokens of the others, never the words.
	for _, path := range []string{"/driver/chester/log", "/driver/chester/log.json"} {
		body := doAs(t, app, boss, "GET", path, nil).Body.String()
		for _, leak := range []string{"cannabis", "KRIS-ANSWER", "best tacos", "DANA-ANSWER", "an old question", "OLD-ANSWER"} {
			if strings.Contains(body, leak) {
				t.Errorf("the admin's %s shows %q", path, leak)
			}
		}
	}
	var export struct {
		Queries []ChesterQuery `json:"queries"`
		Others  []struct {
			User      string  `json:"user"`
			AskedAt   string  `json:"asked_at"`
			USD       float64 `json:"usd"`
			TokensIn  int64   `json:"tokens_in"`
			TokensOut int64   `json:"tokens_out"`
		} `json:"others"`
	}
	if err := json.Unmarshal(doAs(t, app, boss, "GET", "/driver/chester/log.json", nil).Body.Bytes(), &export); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, o := range export.Others {
		seen[o.User] = o.AskedAt != "" && o.USD > 0 && o.TokensIn == 111 && o.TokensOut == 222
	}
	if len(export.Queries) != 0 || !seen["kris"] || !seen["dana"] || !seen["unattributed"] {
		t.Errorf("the admin sees the others' figures only: own=%d others=%+v", len(export.Queries), export.Others)
	}
	// Only the asker may rule on an answer.
	target := "/driver/chester/log/" + strconv.FormatInt(krisID, 10)
	if w := doAs(t, app, dana, "POST", target, url.Values{"verdict": {"right"}}); w.Code == 200 {
		t.Errorf("dana ruled on kris's answer: %d", w.Code)
	}
	if w := doAs(t, app, boss, "POST", target, url.Values{"verdict": {"right"}}); w.Code == 200 {
		t.Errorf("an admin ruled on an answer they may not read: %d", w.Code)
	}
	if r, _ := app.store.ChesterQuery(krisID); r.Verdict != "" {
		t.Fatalf("the verdict was saved: %q", r.Verdict)
	}
	if w := doAs(t, app, kris, "POST", target, url.Values{"verdict": {"right"}}); w.Code != 200 {
		t.Errorf("kris could not rule on his own: %d", w.Code)
	}
}

func TestAskIsLoggedWithItsDriverAndServerLogsHoldNoWords(t *testing.T) {
	app := chesterApp(t, newAPIStub(t, quickScript()))
	kris := asUser(t, app, "kris", roleEditor)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	w := doAs(t, app, kris, "POST", "/chester/ask", url.Values{"q": {"SECRET-QUESTION-WORDS about dispensaries"}})
	if w.Code != http.StatusOK {
		t.Fatalf("ask: %d %s", w.Code, w.Body.String())
	}
	rows := chesterRows(t, app)
	if len(rows) != 1 || rows[0].User != "kris" {
		t.Fatalf("the row names its driver: %+v", rows)
	}
	for _, leak := range []string{"SECRET-QUESTION-WORDS", "dispensaries", rows[0].Answer} {
		if leak != "" && strings.Contains(buf.String(), leak) {
			t.Errorf("a server log line holds %q:\n%s", leak, buf.String())
		}
	}
}

func TestChesterTreatsLegalSensitiveTravelQuestionsAsOrdinaryTravelHelp(t *testing.T) {
	for _, want := range []string{"cannabis", "differs by state", "federal", "lecture"} {
		if !strings.Contains(chesterSystemPrompt, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
}
