package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const ttKey = "tt-key-for-tests-only"

// ttStub is TomTom: search, routing and along-route search, counting calls.
type ttStub struct {
	srv                  *httptest.Server
	geo, route, along    atomic.Int32
	geoBody, routeStatus string
	routeBody            string
	alongBody            map[string]string // by query word; "500" is a failure
	mu                   sync.Mutex
	alongReq             []string
	sawKey               atomic.Bool
}

const geoOK = `{"results":[{"poi":{"name":"Hudson Bend Coffee"},"address":{"freeformAddress":"Hudson Bend, TX"},"position":{"lat":30.4,"lon":-97.9}}]}`
const routeOK = `{"routes":[{"summary":{"lengthInMeters":24140,"travelTimeInSeconds":1500,"trafficDelayInSeconds":300,"arrivalTime":"2026-11-07T15:40:00-06:00"},
 "legs":[{"points":[{"latitude":30.20,"longitude":-97.80},{"latitude":30.25,"longitude":-97.84},{"latitude":30.30,"longitude":-97.88},{"latitude":30.35,"longitude":-97.90},{"latitude":30.40,"longitude":-97.90}]}]}]}`

func newTT(t *testing.T) *ttStub {
	s := &ttStub{geoBody: geoOK, alongBody: map[string]string{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") == ttKey {
			s.sawKey.Store(true)
		}
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/search/2/searchAlongRoute/"):
			s.along.Add(1)
			var b struct {
				Route struct {
					Points []map[string]float64 `json:"points"`
				} `json:"route"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			if r.Method != http.MethodPost || len(b.Route.Points) == 0 {
				t.Errorf("along-route must POST route points: %s %d", r.Method, len(b.Route.Points))
			}
			q := strings.TrimSuffix(strings.TrimPrefix(p, "/search/2/searchAlongRoute/"), ".json")
			q, _ = url.PathUnescape(q)
			s.mu.Lock()
			s.alongReq = append(s.alongReq, q+"|"+r.URL.Query().Get("maxDetourTime"))
			s.mu.Unlock()
			body, known := s.alongBody[q]
			if !known {
				body = `{"results":[]}`
			}
			if body == "500" {
				http.Error(w, "boom "+r.URL.RawQuery, 500)
				return
			}
			w.Write([]byte(body))
		case strings.HasPrefix(p, "/search/2/search/"):
			s.geo.Add(1)
			w.Write([]byte(s.geoBody))
		case strings.HasPrefix(p, "/routing/1/calculateRoute/"):
			s.route.Add(1)
			if r.URL.Query().Get("traffic") != "true" {
				t.Errorf("route must ask for traffic: %s", r.URL.RawQuery)
			}
			if s.routeStatus != "" {
				http.Error(w, "boom "+r.URL.RawQuery, 500)
				return
			}
			if s.routeBody != "" {
				w.Write([]byte(s.routeBody))
				return
			}
			w.Write([]byte(routeOK))
		case strings.HasPrefix(p, "/traffic/"):
			w.Write([]byte(`{"incidents":[]}`))
		default:
			t.Errorf("unexpected TomTom path %s", p)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func freeApp(t *testing.T, tt *ttStub) (*App, *http.Cookie) {
	app := testApp(t)
	app.free = newFreeSvc(ttKey)
	app.free.ttBase = tt.srv.URL
	nws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"features":[]}`)) }))
	t.Cleanup(nws.Close)
	app.hazards = hazardSvcFor(nws, tt.srv, ttKey)
	return app, asUser(t, app, "kris", roleAdmin)
}

func goDestined(t *testing.T, app *App, c *http.Cookie, dest string) {
	t.Helper()
	if w := doAs(t, app, c, "POST", "/driver/freelance/dest", url.Values{"dest": {dest}}); w.Code != http.StatusSeeOther {
		t.Fatalf("dest: %d %s", w.Code, w.Body.String())
	}
}

func hudAt(t *testing.T, app *App, c *http.Cookie, at string) hudView {
	t.Helper()
	w := doAs(t, app, c, "GET", "/driver/hud.json"+at, nil)
	if w.Code != 200 {
		t.Fatalf("hud.json %d", w.Code)
	}
	if strings.Contains(w.Body.String(), ttKey) {
		t.Fatal("the TomTom key is in the HUD answer")
	}
	var h hudView
	if err := json.Unmarshal(w.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestFreeModeIsTripByDefaultPerUserAndLeavesThePlanAlone(t *testing.T) {
	tt := newTT(t)
	app, kris := freeApp(t, tt)
	other := asUser(t, app, "lars", roleAdmin)
	body := doAs(t, app, kris, "GET", "/driver", nil).Body.String()
	if !strings.Contains(body, "FreeLance: Destined") || strings.Contains(body, `id="hud-free"`) {
		t.Fatal("default must be the Trip with a switch offered")
	}
	goDestined(t, app, kris, "hudson bend coffee")
	if !strings.Contains(doAs(t, app, kris, "GET", "/driver", nil).Body.String(), `id="hud-free"`) {
		t.Fatal("kris should be on the FreeLance screen")
	}
	if strings.Contains(doAs(t, app, other, "GET", "/driver", nil).Body.String(), `id="hud-free"`) {
		t.Fatal("another user's mode must not follow kris")
	}
	if h := hudAt(t, app, other, ""); h.Free != nil || h.LegSlug == "" {
		t.Fatalf("the other user still sees the planned ride: %+v", h)
	}
	doAs(t, app, kris, "POST", "/driver/freelance/mode", url.Values{"mode": {"trip"}})
	if h := hudAt(t, app, kris, ""); h.Free != nil {
		t.Fatal("switching back to Trip must show the plan")
	}
}

func TestFreeDestinedHUDGivesDistanceETAWazeAndHazardsAlongTheRoute(t *testing.T) {
	tt := newTT(t)
	app, c := freeApp(t, tt)
	var pts []string
	var mu sync.Mutex
	nws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		pts = append(pts, r.URL.Query().Get("point"))
		mu.Unlock()
		w.Write([]byte(`{"features":[{"properties":{"id":"a","event":"Flood Watch","areaDesc":"Travis"}}]}`))
	}))
	defer nws.Close()
	app.hazards = hazardSvcFor(nws, tt.srv, ttKey)
	goDestined(t, app, c, "hudson bend")
	h := hudAt(t, app, c, "?lat=30.2000&lng=-97.8000")
	f := h.Free
	if f == nil || f.State != "ok" || !f.CanPicks {
		t.Fatalf("free view: %+v", f)
	}
	if f.ETA != "3:40 PM" || !strings.HasSuffix(f.Miles, " mi") || f.Miles == "0 mi" || !strings.Contains(f.Delay, "5 min") {
		t.Fatalf("distance/eta/delay: %+v", f)
	}
	if !strings.HasPrefix(f.Waze, "https://waze.com/ul?") || !strings.Contains(f.Waze, "ll=30.4%2C-97.9") {
		t.Fatalf("waze to the destination: %q", f.Waze)
	}
	if f.Google != "https://www.google.com/maps/dir/?api=1&destination=30.4,-97.9" ||
		!strings.HasPrefix(f.Apple, "https://maps.apple.com/?daddr=30.4,-97.9&q=") {
		t.Fatalf("google and apple links to the destination: %q %q", f.Google, f.Apple)
	}
	if !strings.Contains(chipTexts(h.Hazards), "Flood Watch") {
		t.Fatalf("hazards: %s", chipTexts(h.Hazards))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(pts) == 0 {
		t.Fatal("weather was not asked along the route")
	}
	for _, p := range pts {
		if !strings.HasPrefix(p, "30.") || !strings.Contains(p, ",-97.") {
			t.Errorf("weather point %q is not on the destined route", p)
		}
	}
	if tt.along.Load() != 0 {
		t.Fatal("picks must never be fetched by a HUD refresh")
	}
	hudAt(t, app, c, "?lat=30.2000&lng=-97.8000")
	if tt.geo.Load() != 1 || tt.route.Load() != 1 {
		t.Fatalf("geocode and route are cached per user and destination: geo=%d route=%d", tt.geo.Load(), tt.route.Load())
	}
}

func TestFreeNoFixSaysSoAndOffersWazeOnly(t *testing.T) {
	tt := newTT(t)
	app, c := freeApp(t, tt)
	goDestined(t, app, c, "hudson bend")
	f := hudAt(t, app, c, "").Free
	if f.State != "no_fix" || !strings.Contains(f.Text, "No position fix") || f.Waze == "" || f.CanPicks || f.Miles != "—" || f.ETA != "—" {
		t.Fatalf("no fix: %+v", f)
	}
	if tt.route.Load() != 0 {
		t.Fatal("no fix, no routing call")
	}
}

func TestFreeFailuresAreHonestAndNeverLeakTheKey(t *testing.T) {
	tt := newTT(t)
	app, c := freeApp(t, tt)
	tt.geoBody = `{"results":[]}`
	goDestined(t, app, c, "nowhere at all")
	page := doAs(t, app, c, "GET", "/driver", nil).Body.String()
	if !strings.Contains(page, "found no place called that") || strings.Contains(page, ttKey) {
		t.Fatal("an unknown place must be said plainly")
	}
	if f := hudAt(t, app, c, "?lat=30.2&lng=-97.8").Free; f.State != "need_dest" {
		t.Fatalf("%+v", f)
	}
	tt.geoBody = geoOK
	tt.routeStatus = "500"
	goDestined(t, app, c, "hudson bend")
	f := hudAt(t, app, c, "?lat=30.2&lng=-97.8").Free
	if f.State != "route_unavailable" || !strings.Contains(f.Text, "status 500") || f.Waze == "" || f.CanPicks {
		t.Fatalf("route failure: %+v", f)
	}
	h := hudAt(t, app, c, "?lat=30.2&lng=-97.8")
	if !strings.Contains(chipTexts(h.Hazards), "unknown") {
		t.Fatalf("with no route the hazards say unknown, not clear: %s", chipTexts(h.Hazards))
	}
}

func TestFreeRoutesAreSignedInOnly(t *testing.T) {
	tt := newTT(t)
	app, _ := freeApp(t, tt)
	for _, p := range []string{"/driver/freelance/mode", "/driver/freelance/dest", "/driver/freelance/picks"} {
		if w := doAs(t, app, nil, "POST", p, url.Values{}); w.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", p, w.Code)
		}
	}
}

// --- picks ---

const coffeeRes = `{"results":[
 {"poi":{"name":"Big Chain Coffee"},"address":{"municipality":"Leander","countrySubdivision":"TX"},"position":{"lat":30.3,"lon":-97.8},"detourTime":420},
 {"poi":{"name":"Old Mill Roasters"},"address":{"municipality":"Liberty Hill","countrySubdivision":"TX"},"position":{"lat":30.31,"lon":-97.81},"detourTime":130}]}`

func messageJSON(text string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": stubModel,
			"content":     []map[string]any{{"type": "text", "text": text}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 1000, "output_tokens": 200, "server_tool_use": map[string]any{"web_search_requests": 2}}})
	}
}

func picksApp(t *testing.T, tt *ttStub, anthropic *apiStub) (*App, *http.Cookie) {
	app, c := freeApp(t, tt)
	if anthropic != nil {
		app.chester = newChester(chesterEnv(t, anthropic, nil))
	}
	goDestined(t, app, c, "hudson bend")
	return app, c
}

func findPicks(t *testing.T, app *App, c *http.Cookie, fix bool) freePicksOut {
	t.Helper()
	form := url.Values{}
	if fix {
		form = url.Values{"lat": {"30.2000"}, "lng": {"-97.8000"}}
	}
	w := doAs(t, app, c, "POST", "/driver/freelance/picks", form)
	if w.Code != 200 || strings.Contains(w.Body.String(), ttKey) {
		t.Fatalf("picks %d (key leaked? %v)", w.Code, strings.Contains(w.Body.String(), ttKey))
	}
	var o freePicksOut
	if err := json.Unmarshal(w.Body.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func allQueries(c freeCat) []string { return c.Queries }

// fillAll answers every query of every category with the same two places.
func fillAll(tt *ttStub) {
	for _, c := range freeCats {
		for _, q := range allQueries(c) {
			tt.alongBody[q] = coffeeRes
		}
	}
}

const allNotes = `{"picks":[{"id":"coffee-1","note":"a"},{"id":"food-1","note":"b"},{"id":"music-1","note":"c"},{"id":"evening-1","note":"e"},{"id":"outdoors-1","note":"d"},{"id":"culture-1","note":"f"}]}`

func TestFreePicksSpanTheCategoriesHonestStatesAndCost(t *testing.T) {
	tt := newTT(t)
	tt.alongBody["coffee shop"] = coffeeRes
	tt.alongBody["museum"] = coffeeRes // Chester will drop these
	tt.alongBody["state park"] = "500" // outdoors: one source failed and nothing else answered
	stub := newAPIStub(t, messageJSON(`Sure: {"picks":[{"id":"coffee-1","note":"Small roaster, local owner."},{"id":"ghost-9","note":"invented"}]}`))
	app, c := picksApp(t, tt, stub)

	o := findPicks(t, app, c, true)
	keys := map[string]freeCatOut{}
	var order []string
	for _, k := range o.Cats {
		keys[k.Key] = k
		order = append(order, k.Key)
	}
	if o.State != "ok" || o.Chester != "ok" || strings.Join(order, ",") != "coffee,food,music,outdoors,culture" {
		t.Fatalf("the daytime categories, in order, no evening music at 3:40 pm: %v %+v", order, o)
	}
	cof := keys["coffee"]
	if cof.State != "items" || len(cof.Picks) != 1 || cof.Picks[0].Name != "Old Mill Roasters" ||
		cof.Picks[0].Note != "Small roaster, local owner." || cof.Picks[0].Detour != "2 min detour" ||
		!strings.HasPrefix(cof.Picks[0].Waze, "https://waze.com/ul?") ||
		!strings.HasPrefix(cof.Picks[0].Google, "https://www.google.com/maps/dir/?api=1&destination=30.31,-97.81") ||
		!strings.HasPrefix(cof.Picks[0].Apple, "https://maps.apple.com/?daddr=30.31,-97.81&q=Old+Mill+Roasters") {
		t.Fatalf("coffee: %+v", cof)
	}
	if f := keys["food"]; f.State != "empty" || !strings.Contains(f.Text, "no standout food") {
		t.Fatalf("food: %+v", f)
	}
	if m := keys["culture"]; m.State != "empty" || !strings.Contains(m.Text, "worth the stop") {
		t.Fatalf("Chester may drop dull ones: %+v", m)
	}
	if u := keys["outdoors"]; u.State != "unavailable" || !strings.Contains(u.Text, "unavailable") || strings.Contains(u.Text, "none") {
		t.Fatalf("outdoors: %+v", u)
	}
	if !tt.sawKey.Load() {
		t.Fatal("TomTom was never sent the key")
	}
	for _, q := range tt.alongReq {
		if !strings.HasSuffix(q, "|900") {
			t.Errorf("detour cap not sent: %s", q)
		}
	}
	if n := len(stub.requests()); n != 1 {
		t.Fatalf("anthropic calls: %d", n)
	}
	raw := string(stub.requests()[0].Body)
	for _, want := range []string{`"max_uses":3`, "web_search", "not chains", "CATEGORY coffee", "CATEGORY culture", "museums, historic sites"} {
		if !strings.Contains(raw, want) {
			t.Errorf("request lacks %q", want)
		}
	}
	if strings.Contains(raw, ttKey) {
		t.Fatal("the TomTom key went to Anthropic")
	}
	rows := chesterRows(t, app)
	if len(rows) != 1 || rows[0].Mode != "picks" || rows[0].USD <= 0 || rows[0].Searches != 2 || rows[0].PromptVersion != freePicksVersion {
		t.Fatalf("log: %+v", rows)
	}
}

func TestFreeFoodMergesCuisinesAndDropsTheSamePlaceTwice(t *testing.T) {
	tt := newTT(t)
	tt.alongBody["barbecue"] = `{"results":[{"poi":{"name":"Smoke Shack"},"address":{"municipality":"Lockhart","countrySubdivision":"TX"},"position":{"lat":30.3,"lon":-97.8},"detourTime":300}]}`
	tt.alongBody["indian restaurant"] = `{"results":[{"poi":{"name":"Smoke Shack"},"address":{"municipality":"Lockhart","countrySubdivision":"TX"},"position":{"lat":30.3,"lon":-97.8},"detourTime":300},
 {"poi":{"name":"Masala House"},"address":{"municipality":"Austin","countrySubdivision":"TX"},"position":{"lat":30.32,"lon":-97.82},"detourTime":200}]}`
	app, c := picksApp(t, tt, nil)
	o := findPicks(t, app, c, true)
	var food freeCatOut
	for _, k := range o.Cats {
		if k.Key == "food" {
			food = k
		}
	}
	if len(food.Picks) != 2 || food.Picks[0].Name != "Masala House" || food.Picks[1].Name != "Smoke Shack" || food.Picks[0].ID != "food-1" {
		t.Fatalf("food: %+v", food)
	}
}

func eveningRoute(arrival string, far bool) string {
	last := `{"latitude":30.40,"longitude":-97.90}`
	if far {
		last = `{"latitude":35.40,"longitude":-97.90}`
	}
	return `{"routes":[{"summary":{"lengthInMeters":24140,"travelTimeInSeconds":1500,"trafficDelayInSeconds":0,"arrivalTime":"` + arrival + `"},
 "legs":[{"points":[{"latitude":30.20,"longitude":-97.80},{"latitude":30.30,"longitude":-97.88},` + last + `]}]}]}`
}

func catKeys(o freePicksOut) string {
	var ks []string
	for _, k := range o.Cats {
		ks = append(ks, k.Key)
	}
	return strings.Join(ks, ",")
}

func TestFreeEveningMusicOnlyNearTheEndOfDayWithAnEveningETA(t *testing.T) {
	for _, tc := range []struct {
		name, arrival string
		far, want     bool
	}{
		{"near and evening", "2026-11-07T18:40:00-06:00", false, true},
		{"near but mid-afternoon", "2026-11-07T15:40:00-06:00", false, false},
		{"evening but far away", "2026-11-07T18:40:00-06:00", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tt := newTT(t)
			tt.routeBody = eveningRoute(tc.arrival, tc.far)
			app, c := picksApp(t, tt, nil)
			got := strings.Contains(catKeys(findPicks(t, app, c, true)), "evening")
			if got != tc.want {
				t.Fatalf("evening category present=%v want %v", got, tc.want)
			}
		})
	}
}

func clockAt(app *App, ts string) {
	t, _ := time.Parse(time.RFC3339, ts)
	app.free.now = func() time.Time { return t }
}

func TestFreePlacesAreCachedADayAndSharedAcrossUsers(t *testing.T) {
	tt := newTT(t)
	fillAll(tt)
	stub := newAPIStub(t, messageJSON(allNotes), messageJSON(allNotes), messageJSON(allNotes))
	app, c := picksApp(t, tt, stub)
	clockAt(app, "2026-11-07T12:00:00Z")
	findPicks(t, app, c, true)
	calls := tt.along.Load()
	if calls < 10 {
		t.Fatalf("a cold tap asks TomTom per query: %d", calls)
	}
	o := findPicks(t, app, c, true)
	if !o.Cached || tt.along.Load() != calls || len(stub.requests()) != 1 {
		t.Fatalf("second tap comes from the cache: cached=%v along=%d chester=%d", o.Cached, tt.along.Load(), len(stub.requests()))
	}
	// Another signed-in user on the same stretch reuses it too.
	c2 := asUser(t, app, "dana", roleAdmin)
	goDestined(t, app, c2, "hudson bend")
	o2 := findPicks(t, app, c2, true)
	if !o2.Cached || tt.along.Load() != calls || len(stub.requests()) != 1 || o2.Cats[0].Picks[0].Note != "a" {
		t.Fatalf("shared across users: cached=%v along=%d chester=%d", o2.Cached, tt.along.Load(), len(stub.requests()))
	}
	// 23h later still cached (music has its own, shorter day).
	clockAt(app, "2026-11-08T11:00:00Z")
	findPicks(t, app, c, true)
	coffeeCalls := 0
	for _, q := range tt.alongReq {
		if strings.HasPrefix(q, "coffee shop|") {
			coffeeCalls++
		}
	}
	if coffeeCalls != 1 {
		t.Fatalf("coffee must be cached for 24 hours: %d calls", coffeeCalls)
	}
	// Past 24 hours it is asked again.
	clockAt(app, "2026-11-08T12:00:01Z")
	findPicks(t, app, c, true)
	coffeeCalls = 0
	for _, q := range tt.alongReq {
		if strings.HasPrefix(q, "coffee shop|") {
			coffeeCalls++
		}
	}
	if coffeeCalls != 2 {
		t.Fatalf("coffee must be asked again after 24 hours: %d calls", coffeeCalls)
	}
}

func TestFreeMusicIsCachedOnlyUntilTheEndOfTheLocalDay(t *testing.T) {
	tt := newTT(t)
	fillAll(tt)
	stub := newAPIStub(t, messageJSON(allNotes), messageJSON(allNotes))
	app, c := picksApp(t, tt, stub)
	count := func(prefix string) int {
		n := 0
		for _, q := range tt.alongReq {
			if strings.HasPrefix(q, prefix) {
				n++
			}
		}
		return n
	}
	// The stub's ETA is 3:40 pm at -06:00, so that local day ends 06:00 UTC.
	clockAt(app, "2026-11-08T05:00:00Z")
	findPicks(t, app, c, true)
	clockAt(app, "2026-11-08T05:29:00Z") // inside the notes' half hour
	findPicks(t, app, c, true)
	if count("live music bar|") != 1 || count("coffee shop|") != 1 {
		t.Fatalf("before midnight local both are cached: music=%d coffee=%d", count("live music bar|"), count("coffee shop|"))
	}
	clockAt(app, "2026-11-08T06:01:00Z")
	findPicks(t, app, c, true)
	if count("live music bar|") != 2 || count("coffee shop|") != 1 {
		t.Fatalf("after local midnight music is asked again, coffee is not: music=%d coffee=%d", count("live music bar|"), count("coffee shop|"))
	}
	if n := len(stub.requests()); n != 2 {
		t.Fatalf("Chester's notes last half an hour, so the 06:01 look ranks again and the 05:29 one did not: %d calls", n)
	}
}

func TestFreeCandidatesAreKeptWhenChesterWasOffAndRankedLater(t *testing.T) {
	tt := newTT(t)
	fillAll(tt)
	app, c := picksApp(t, tt, nil)
	findPicks(t, app, c, true)
	calls := tt.along.Load()
	stub := newAPIStub(t, messageJSON(allNotes))
	app.chester = newChester(chesterEnv(t, stub, nil))
	o := findPicks(t, app, c, true)
	if tt.along.Load() != calls || o.Chester != "ok" || o.Cached || o.Cats[0].Picks[0].Note != "a" || len(stub.requests()) != 1 {
		t.Fatalf("TomTom is not asked again, Chester ranks now: along %d->%d %+v", calls, tt.along.Load(), o)
	}
}

func TestFreePicksWithChesterOffSayTheyAreUnranked(t *testing.T) {
	tt := newTT(t)
	tt.alongBody["coffee shop"] = coffeeRes
	app, c := picksApp(t, tt, nil)
	o := findPicks(t, app, c, true)
	if o.Chester != "off" || !strings.Contains(o.Note, "Chester is off") || len(o.Cats[0].Picks) != 2 || o.Cats[0].Picks[0].Note != "" {
		t.Fatalf("%+v", o)
	}
}

func TestFreePicksChesterFailureKeepsTomTomsCandidates(t *testing.T) {
	tt := newTT(t)
	tt.alongBody["coffee shop"] = coffeeRes
	stub := newAPIStub(t, apiError(400, "invalid_request_error", "nope "+stubKey))
	app, c := picksApp(t, tt, stub)
	o := findPicks(t, app, c, true)
	if o.Chester != "error" || !strings.Contains(o.Note, "could not rank") || len(o.Cats[0].Picks) != 2 {
		t.Fatalf("%+v", o)
	}
	if rows := chesterRows(t, app); len(rows) != 1 || rows[0].Error == "" || strings.Contains(rows[0].Error, stubKey) {
		t.Fatalf("the failed call is logged without the key: %+v", rows)
	}
}

func TestFreePicksNeedAFixAndADestination(t *testing.T) {
	tt := newTT(t)
	app, c := picksApp(t, tt, nil)
	if o := findPicks(t, app, c, false); o.State != "no_fix" || len(o.Cats) != 0 {
		t.Fatalf("%+v", o)
	}
	app2, c2 := freeApp(t, tt)
	if o := findPicks(t, app2, c2, true); o.State != "need_dest" {
		t.Fatalf("%+v", o)
	}
	if tt.along.Load() != 0 {
		t.Fatal("no along-route call without a fix and a destination")
	}
}

func TestEveryStopPageOffersWazeGoogleAndApple(t *testing.T) {
	app := testApp(t)
	c := asUser(t, app, "kris", roleAdmin)
	for _, path := range []string{"/", "/driver"} {
		body := doAs(t, app, c, "GET", path, nil).Body.String()
		for _, want := range []string{"waze.com/ul?", "google.com/maps/dir/?api=1&amp;destination=", "maps.apple.com/?daddr="} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %s", path, want)
			}
		}
	}
}
