package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// evToolUse is a tool call of any name, streamed the way evPlaces streams show_places.
func evToolUse(idx int, id, name, input string) []string {
	return evBlock(idx, map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}},
		map[string]any{"type": "input_json_delta", "partial_json": input})
}

// rememberScript answers with words and one remember call.
func rememberScript(words, notes string) http.HandlerFunc {
	return stream(events(
		evStart(stubModel, 200, 900, 0),
		evText(0, words),
		evToolUse(1, "toolu_mem", chesterRememberTool, notes),
		evEnd("tool_use", 50, 0, nil),
	)...)
}

func TestMemoryStoreKeepsTheLastTurnsAndCapsLeaningsPerUser(t *testing.T) {
	app := testApp(t)
	s := app.store
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= chesterTurnsKept+2; i++ {
		if err := s.AddChesterTurn("kris", fmt.Sprintf("q%d", i), fmt.Sprintf("a%d", i), at.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	turns, _ := s.ChesterTurns("kris")
	if len(turns) != chesterTurnsKept || turns[0].Q != "q3" || turns[len(turns)-1].Q != fmt.Sprintf("q%d", chesterTurnsKept+2) {
		t.Fatalf("oldest dropped, oldest first: %+v", turns)
	}
	for i := 1; i <= chesterLeaningsKept+5; i++ {
		_ = s.AddChesterLeanings("kris", []string{fmt.Sprintf("note %d", i)}, at.Add(time.Duration(i)*time.Minute))
	}
	_ = s.AddChesterLeanings("kris", []string{"NOTE 40"}, at.Add(time.Hour)) // a repeat, other case
	ls, _ := s.ChesterLeanings("kris")
	if len(ls) != chesterLeaningsKept || ls[0].Note != "note 7" || strings.ToLower(ls[len(ls)-1].Note) != "note 40" {
		t.Fatalf("cap, oldest dropped, repeat not doubled: %d first=%q last=%q", len(ls), ls[0].Note, ls[len(ls)-1].Note)
	}
	_ = s.AddChesterTurn("ann", "ann q", "ann a", at)
	_ = s.AddChesterLeanings("ann", []string{"ann likes tea"}, at)
	if err := s.ClearChesterMemory("kris"); err != nil {
		t.Fatal(err)
	}
	if ts, _ := s.ChesterTurns("kris"); len(ts) != 0 {
		t.Fatal("clear left turns")
	}
	if ls, _ := s.ChesterLeanings("kris"); len(ls) != 0 {
		t.Fatal("clear left leanings")
	}
	if ts, _ := s.ChesterTurns("ann"); len(ts) != 1 {
		t.Fatal("clearing one user's memory touched another's")
	}
}

func TestParseLeaningsIsBoundedAndRefusesAnythingThatLooksPrivate(t *testing.T) {
	got := parseLeanings(`{"notes":["prefers local diners over chains","  ","call 5125551234 ","see https://x.example","a@b.example",
		"` + strings.Repeat("long ", 60) + `","four","five","six"]}`)
	if len(got) != chesterLeaningsPerCall || got[0] != "prefers local diners over chains" {
		t.Fatalf("got %q", got)
	}
	for _, n := range got {
		if len(n) > chesterLeaningMax {
			t.Errorf("too long: %q", n)
		}
	}
	if parseLeanings(`not json`) != nil {
		t.Fatal("garbage gives nothing")
	}
}

func TestAskCarriesTheDestinedDriveAndLearnsAndRemembers(t *testing.T) {
	tt := newTT(t)
	stub := newAPIStub(t,
		rememberScript("Try Old Mill Roasters. Check the hours.", `{"notes":["prefers local diners over chains"]}`),
		stream(events(evStart(stubModel, 200, 900, 0), evText(0, "Second answer."), evEnd("end_turn", 20, 0, nil))...))
	app, c := picksApp(t, tt, stub)

	form := url.Values{"q": {"Where is a good bite to eat?"}, "lat": {"30.2000"}, "lng": {"-97.8000"}}
	if w := doAs(t, app, c, "POST", "/chester/ask", form); w.Code != 200 {
		t.Fatalf("ask: %d %s", w.Code, w.Body.String())
	}
	first := string(stub.requests()[0].Body)
	for _, want := range []string{"DRIVE: FreeLance Destined", "Hudson Bend", "3:40 PM", "NOW:", chesterRememberTool} {
		if !strings.Contains(first, want) {
			t.Errorf("first request lacks %q", want)
		}
	}
	if strings.Contains(first, "WHAT YOU REMEMBER") {
		t.Error("nothing is remembered yet, so nothing is said")
	}
	ls, _ := app.store.ChesterLeanings("kris")
	ts, _ := app.store.ChesterTurns("kris")
	if len(ls) != 1 || ls[0].Note != "prefers local diners over chains" || len(ts) != 1 || !strings.Contains(ts[0].A, "Old Mill") {
		t.Fatalf("learned: %+v %+v", ls, ts)
	}
	if w := doAs(t, app, c, "POST", "/chester/ask", url.Values{"q": {"And fuel?"}}); w.Code != 200 {
		t.Fatalf("second ask: %d", w.Code)
	}
	second := string(stub.requests()[1].Body)
	for _, want := range []string{"WHAT YOU REMEMBER", "prefers local diners over chains", "Where is a good bite to eat?", "Old Mill Roasters"} {
		if !strings.Contains(second, want) {
			t.Errorf("second request lacks %q", want)
		}
	}
	rows := chesterRows(t, app)
	if len(rows) != 2 || rows[0].USD <= 0 || rows[1].USD <= 0 {
		t.Fatalf("both calls are logged and priced: %+v", rows)
	}
}

func TestAskInTripModeNamesThePlannedRide(t *testing.T) {
	stub := newAPIStub(t, rememberScript("Fine.", `{"notes":[]}`))
	app := testApp(t)
	app.chester = newChester(chesterEnv(t, stub, nil))
	c := asUser(t, app, "kris", roleAdmin)
	if w := doAs(t, app, c, "POST", "/chester/ask", url.Values{"q": {"Anything ahead?"}}); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if body := string(stub.requests()[0].Body); !strings.Contains(body, "DRIVE: the planned ride") {
		t.Fatal("trip mode is named in the drive context")
	}
}

func TestMemoryPageShowsOnlyYoursAndClears(t *testing.T) {
	app := testApp(t)
	kris := asUser(t, app, "kris", roleAdmin)
	ann := asUser(t, app, "ann", roleAdmin)
	at := time.Now()
	_ = app.store.AddChesterLeanings("kris", []string{"skipped BBQ twice today"}, at)
	_ = app.store.AddChesterTurn("kris", "kris asked this", "kris got this", at)
	_ = app.store.AddChesterLeanings("ann", []string{"ann likes tea"}, at)

	w := doAs(t, app, kris, "GET", "/driver/chester/memory", nil)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "skipped BBQ twice today") || !strings.Contains(body, "kris asked this") || strings.Contains(body, "ann likes tea") {
		t.Fatalf("kris's page: %d", w.Code)
	}
	if w := doAs(t, app, nil, "GET", "/driver/chester/memory", nil); w.Code == 200 || strings.Contains(w.Body.String(), "skipped BBQ") {
		t.Fatalf("a visitor must not see memory: %d", w.Code)
	}
	if w := doAs(t, app, nil, "POST", "/driver/chester/memory/clear", url.Values{}); w.Code == 200 || w.Code == 303 && w.Header().Get("Location") == "/driver/chester/memory" {
		t.Fatalf("a visitor must not clear: %d", w.Code)
	}
	if w := doAs(t, app, kris, "POST", "/driver/chester/memory/clear", url.Values{}); w.Code != http.StatusSeeOther {
		t.Fatalf("clear: %d", w.Code)
	}
	if ls, _ := app.store.ChesterLeanings("kris"); len(ls) != 0 {
		t.Fatal("not cleared")
	}
	if w := doAs(t, app, ann, "GET", "/driver/chester/memory", nil); !strings.Contains(w.Body.String(), "ann likes tea") {
		t.Fatal("clearing kris's memory cleared ann's")
	}
}

func TestReAskAlwaysAsksFreshWhileTheCandidatesStayCached(t *testing.T) {
	tt := newTT(t)
	fillAll(tt)
	notes := `{"picks":[{"id":"coffee-1","note":"a"}],"leanings":["likes roasters"]}`
	stub := newAPIStub(t, messageJSON(notes), messageJSON(notes), messageJSON(notes))
	app, c := picksApp(t, tt, stub)
	clockAt(app, "2026-11-07T12:00:00Z")

	findPicks(t, app, c, true)
	findPicks(t, app, c, true)
	if n := len(stub.requests()); n != 1 {
		t.Fatalf("a repeat inside half an hour is served from the cache: %d calls", n)
	}
	along := tt.along.Load()
	w := doAs(t, app, c, "POST", "/driver/freelance/picks",
		url.Values{"lat": {"30.2000"}, "lng": {"-97.8000"}, "force": {"1"}, "want": {"hungry now"}})
	if w.Code != 200 || len(stub.requests()) != 2 {
		t.Fatalf("re-ask asks again: %d, %d calls", w.Code, len(stub.requests()))
	}
	raw := string(stub.requests()[1].Body)
	for _, want := range []string{"hungry now", "DRIVE: FreeLance Destined", "Hudson Bend", "likes roasters"} {
		if !strings.Contains(raw, want) {
			t.Errorf("re-ask request lacks %q", want)
		}
	}
	if tt.along.Load() != along {
		t.Fatal("TomTom candidates keep their own cache")
	}
	if ls, _ := app.store.ChesterLeanings("kris"); len(ls) != 1 || ls[0].Note != "likes roasters" {
		t.Fatalf("leanings from picks are kept: %+v", ls)
	}
	// A line of the driver's own is not cached for the next person to be served.
	findPicks(t, app, c, true)
	if n := len(stub.requests()); n != 2 {
		t.Fatalf("the first ranking was still cached: %d calls", n)
	}
	clockAt(app, "2026-11-07T12:31:00Z")
	findPicks(t, app, c, true)
	if len(stub.requests()) != 3 || tt.along.Load() != along {
		t.Fatalf("notes last about half an hour, places a day: %d calls, %d along", len(stub.requests()), tt.along.Load())
	}
}

func TestDestinedHazardSamplesDropPointsNearFuzzedPlaces(t *testing.T) {
	tt := newTT(t)
	app, c := freeApp(t, tt)
	var mu sync.Mutex
	var pts []string
	nws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		pts = append(pts, r.URL.Query().Get("point"))
		mu.Unlock()
		w.Write([]byte(`{"features":[]}`))
	}))
	defer nws.Close()
	app.hazards = hazardSvcFor(nws, tt.srv, ttKey)
	rv, err := app.loadRide()
	if err != nil {
		t.Fatal(err)
	}
	fuzzed := fuzzedOf(rideCheckpoints(rv.Legs))
	if len(fuzzed) == 0 {
		t.Fatal("the seed has no private place; this test would pass on nothing")
	}
	// The route ends at the seed's private origin: its last vertex is the doorstep.
	f := fuzzed[0]
	tt.routeBody = fmt.Sprintf(`{"routes":[{"summary":{"lengthInMeters":24140,"travelTimeInSeconds":1500,"trafficDelayInSeconds":0,"arrivalTime":"2026-11-07T15:40:00-06:00"},
 "legs":[{"points":[{"latitude":%f,"longitude":%f},{"latitude":%f,"longitude":%f},{"latitude":%f,"longitude":%f},{"latitude":%f,"longitude":%f}]}]}]}`,
		f.Lat-0.5, f.Lng-0.5, f.Lat-0.25, f.Lng-0.25, f.Lat-0.003, f.Lng-0.003, f.Lat, f.Lng)
	goDestined(t, app, c, "hudson bend")
	hudAt(t, app, c, fmt.Sprintf("?lat=%f&lng=%f", f.Lat-0.5, f.Lng-0.5))
	mu.Lock()
	defer mu.Unlock()
	if len(pts) == 0 {
		t.Fatal("no weather point was asked at all")
	}
	for _, p := range pts {
		var la, ln float64
		fmt.Sscanf(p, "%f,%f", &la, &ln)
		if haversineKm(la, ln, f.Lat, f.Lng) < lineMaskKm {
			t.Errorf("weather was asked at %s, within %.0f km of a private place", p, lineMaskKm)
		}
	}
}
