package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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
	near                 atomic.Int32
	nearBody             map[string]string   // poiSearch answers by query word
	nearReq              []map[string]string // lat, lon, radius of each poiSearch call
	geoBody, routeStatus string
	geoReq               []url.Values // the query of each fuzzy-search call
	routeBody            string
	alongBody            map[string]string // by query word; "500" is a failure
	mu                   sync.Mutex
	alongReq             []string
	alongPts             [][]map[string]float64 // route points each along-route call carried
	sawKey               atomic.Bool
}

const geoOK = `{"results":[{"poi":{"name":"Hudson Bend Coffee"},"address":{"freeformAddress":"Hudson Bend, TX"},"position":{"lat":30.4,"lon":-97.9}}]}`
const routeOK = `{"routes":[{"summary":{"lengthInMeters":24140,"travelTimeInSeconds":1500,"trafficDelayInSeconds":300,"arrivalTime":"2026-11-07T15:40:00-06:00"},
 "legs":[{"points":[{"latitude":30.20,"longitude":-97.80},{"latitude":30.25,"longitude":-97.84},{"latitude":30.30,"longitude":-97.88},{"latitude":30.35,"longitude":-97.90},{"latitude":30.40,"longitude":-97.90}]}]}]}`

func newTT(t *testing.T) *ttStub {
	s := &ttStub{geoBody: geoOK, alongBody: map[string]string{}, nearBody: map[string]string{}}
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
			s.alongPts = append(s.alongPts, b.Route.Points)
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
		case strings.HasPrefix(p, "/search/2/poiSearch/"):
			s.near.Add(1)
			q := strings.TrimSuffix(strings.TrimPrefix(p, "/search/2/poiSearch/"), ".json")
			q, _ = url.PathUnescape(q)
			qs := r.URL.Query()
			s.mu.Lock()
			s.nearReq = append(s.nearReq, map[string]string{"q": q, "lat": qs.Get("lat"), "lon": qs.Get("lon"), "radius": qs.Get("radius")})
			s.mu.Unlock()
			body, known := s.nearBody[q]
			if !known {
				body = `{"results":[]}`
			}
			w.Write([]byte(body))
		case strings.HasPrefix(p, "/search/2/search/"):
			s.geo.Add(1)
			s.mu.Lock()
			s.geoReq = append(s.geoReq, r.URL.Query())
			s.mu.Unlock()
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
	if f.ETA != "about 3:40 PM" || !strings.HasSuffix(f.Miles, " mi") || f.Miles == "0 mi" || !strings.Contains(f.Delay, "5 min") {
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
	// The stub's route arrives 2026-11-07 15:40 at -06:00, so the music
	// candidates are kept until 06:00 UTC on the 8th. Pin the clock inside
	// that day, or this passes only while the real date is before it.
	clockAt(app, "2026-11-08T05:00:00Z")
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

// Navigation is offered to the next open stop, never to the start, so the
// drive has to have started for the driver screen to carry any.
func TestEveryStopPageOffersWazeGoogleAndApple(t *testing.T) {
	app := testApp(t)
	c := asUser(t, app, "kris", roleAdmin)
	rv, err := app.loadRide()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetArrived(tripStart(rv.Legs).ID, time.Now().Add(-time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/driver"} {
		body := doAs(t, app, c, "GET", path, nil).Body.String()
		for _, want := range []string{"waze.com/ul?", "google.com/maps/dir/?api=1&amp;destination=", "maps.apple.com/?daddr="} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %s", path, want)
			}
		}
	}
}

// --- the private-place mask on the along-route search ---------------------------

// The fixture trip's Alder (40.10, -100.45) and Elm (44.30, -106.30) are
// fuzzed checkpoints, invented places. A route is written as lat,lng pairs.
func routeJSON(pts [][2]float64) string {
	var sb strings.Builder
	sb.WriteString(`{"routes":[{"summary":{"lengthInMeters":900000,"travelTimeInSeconds":36000,"trafficDelayInSeconds":0,"arrivalTime":"2030-05-01T15:40:00-05:00"},"legs":[{"points":[`)
	for i, p := range pts {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"latitude":` + strconv.FormatFloat(p[0], 'f', 5, 64) + `,"longitude":` + strconv.FormatFloat(p[1], 'f', 5, 64) + `}`)
	}
	sb.WriteString(`]}]}]}`)
	return sb.String()
}

// maskedPicksApp is a Destined app whose active ride is the fixture trip, so
// there are private places to keep out of the search.
func maskedPicksApp(t *testing.T, tt *ttStub, route [][2]float64) (*App, *http.Cookie) {
	t.Helper()
	t.Setenv("PUSH_TOKEN", testToken)
	app, c := freeApp(t, tt)
	if w := pushTrip(t, app, fixtureTrip(t), "?activate=1"); w.Code != http.StatusOK {
		t.Fatalf("push fixture: %d %s", w.Code, w.Body.String())
	}
	tt.routeBody = routeJSON(route)
	tt.alongBody["coffee shop"] = coffeeRes
	goDestined(t, app, c, "hudson bend")
	return app, c
}

func findPicksAt(t *testing.T, app *App, c *http.Cookie, lat, lng string) freePicksOut {
	t.Helper()
	w := doAs(t, app, c, "POST", "/driver/freelance/picks", url.Values{"lat": {lat}, "lng": {lng}})
	var o freePicksOut
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &o) != nil {
		t.Fatalf("picks %d %s", w.Code, w.Body.String())
	}
	return o
}

func TestAlongRouteSearchNeverSendsPointsNearAPrivatePlace(t *testing.T) {
	// Starts at Alder's door, passes it again, runs far, then passes Elm.
	route := [][2]float64{{40.10, -100.45}, {40.12, -100.43}, {40.50, -100.30}, {41.00, -101.00}, {42.00, -102.50},
		{43.00, -104.00}, {44.28, -106.28}, {44.30, -106.31}, {45.00, -107.50}}
	tt := newTT(t)
	app, c := maskedPicksApp(t, tt, route)
	o := findPicksAt(t, app, c, "40.1000", "-100.4500")
	if o.State != "ok" || tt.along.Load() == 0 {
		t.Fatalf("a mostly-open route must still be searched: %+v along=%d", o, tt.along.Load())
	}
	fuzzed := []Checkpoint{{Lat: 40.10, Lng: -100.45}, {Lat: 44.30, Lng: -106.30}}
	tt.mu.Lock()
	defer tt.mu.Unlock()
	for _, pts := range tt.alongPts {
		if len(pts) < 2 {
			t.Fatalf("searchAlongRoute needs 2+ points, got %d", len(pts))
		}
		for _, p := range pts {
			if nearFuzzed(p["lat"], p["lon"], fuzzed) {
				t.Errorf("TomTom was sent %v, within %.0f km of a private place", p, lineMaskKm)
			}
		}
	}
}

func TestAlongRouteSearchFailsClosedWhenTheWholeRouteIsPrivate(t *testing.T) {
	route := [][2]float64{{40.10, -100.45}, {40.12, -100.43}, {40.14, -100.41}, {40.16, -100.39}}
	tt := newTT(t)
	app, c := maskedPicksApp(t, tt, route)
	o := findPicksAt(t, app, c, "40.1000", "-100.4500")
	if tt.along.Load() != 0 {
		t.Fatalf("%d along-route calls were made for a route inside a private radius", tt.along.Load())
	}
	if o.State != "route_unavailable" || len(o.Cats) != 0 || o.Text == "" {
		t.Fatalf("want the fail-closed state, got %+v", o)
	}
}

func TestAlongRouteSearchFarFromAnyPrivatePlaceSendsThePointsAsTheyAre(t *testing.T) {
	route := [][2]float64{{35.00, -90.00}, {35.50, -90.50}, {36.00, -91.00}, {36.50, -91.50}}
	tt := newTT(t)
	app, c := maskedPicksApp(t, tt, route)
	o := findPicksAt(t, app, c, "35.0000", "-90.0000")
	if o.State != "ok" || tt.along.Load() == 0 {
		t.Fatalf("an open route must be searched: %+v", o)
	}
	tt.mu.Lock()
	defer tt.mu.Unlock()
	for _, pts := range tt.alongPts {
		if len(pts) != len(route) {
			t.Fatalf("sent %d points, want all %d unchanged", len(pts), len(route))
		}
		for i, p := range pts {
			if p["lat"] != route[i][0] || p["lon"] != route[i][1] {
				t.Errorf("point %d changed: %v vs %v", i, p, route[i])
			}
		}
	}
}

// --- FreeLance: Aimless ---------------------------------------------------------

const nearRes = `{"results":[
 {"poi":{"name":"Far Chain Coffee"},"address":{"municipality":"Leander","countrySubdivision":"TX"},"position":{"lat":35.05,"lon":-90.05},"dist":6400},
 {"poi":{"name":"Corner Roasters"},"address":{"municipality":"Liberty Hill","countrySubdivision":"TX"},"position":{"lat":35.01,"lon":-90.01},"dist":1200}]}`

func goAimless(t *testing.T, app *App, c *http.Cookie) {
	t.Helper()
	if w := doAs(t, app, c, "POST", "/driver/freelance/mode", url.Values{"mode": {"aimless"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("mode: %d %s", w.Code, w.Body.String())
	}
}

// aimlessApp is an app on the fixture trip (so there are private places to
// keep out of the search) with kris in Aimless.
func aimlessApp(t *testing.T, tt *ttStub) (*App, *http.Cookie) {
	t.Helper()
	t.Setenv("PUSH_TOKEN", testToken)
	app, c := freeApp(t, tt)
	if w := pushTrip(t, app, fixtureTrip(t), "?activate=1"); w.Code != http.StatusOK {
		t.Fatalf("push fixture: %d %s", w.Code, w.Body.String())
	}
	tt.nearBody["coffee shop"] = nearRes
	goAimless(t, app, c)
	return app, c
}

func TestAimlessModeIsPerUserAndDrawsItsOwnScreen(t *testing.T) {
	tt := newTT(t)
	app, kris := freeApp(t, tt)
	other := asUser(t, app, "lars", roleAdmin)
	if body := doAs(t, app, kris, "GET", "/driver", nil).Body.String(); !strings.Contains(body, `value="aimless"`) ||
		strings.Contains(body, `value="aimless" aria-current="true"`) {
		t.Fatal("Trip must offer the Aimless button without marking it current")
	}
	goAimless(t, app, kris)
	body := doAs(t, app, kris, "GET", "/driver", nil).Body.String()
	for _, want := range []string{"FreeLance: Aimless", `value="aimless" aria-current="true"`, `id="hud-aimless"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the Aimless screen lacks %q", want)
		}
	}
	for _, not := range []string{`id="hud-free"`, `id="hud-day"`, `id="hud-next"`, `value="destined" aria-current`, `value="trip" aria-current`} {
		if strings.Contains(body, not) {
			t.Errorf("the Aimless screen carries %q", not)
		}
	}
	if o := doAs(t, app, other, "GET", "/driver", nil).Body.String(); strings.Contains(o, `id="hud-aimless"`) {
		t.Fatal("another user's mode must not follow kris")
	}
	if h := hudAt(t, app, other, ""); h.Free != nil || h.LegSlug == "" {
		t.Fatalf("the other user still sees the planned ride: %+v", h)
	}
}

func TestAimlessHUDShowsNoPlanNextNoETAAndHazardsAroundTheFix(t *testing.T) {
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
	goAimless(t, app, c)
	h := hudAt(t, app, c, "?lat=35.0000&lng=-90.0000")
	if h.Free == nil || h.Free.State != "aimless" {
		t.Fatalf("free view: %+v", h.Free)
	}
	if h.HasNext || h.NextName != "" || h.NextMiles != "" || h.ETA != "" || h.LegSlug != "" || h.TonightName != "" || h.Waze != "" {
		t.Fatalf("Aimless must carry nothing from the plan: %+v", h)
	}
	if h.Free.ETA != "—" || h.Free.Waze != "" || h.Free.CanPicks != true {
		t.Fatalf("no ETA and no route, but picks on offer with a fix: %+v", h.Free)
	}
	if !strings.Contains(chipTexts(h.Hazards), "Flood Watch") {
		t.Fatalf("hazards: %s", chipTexts(h.Hazards))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(pts) == 0 {
		t.Fatal("weather was not asked around the fix")
	}
	for _, p := range pts {
		var la, ln float64
		parts := strings.Split(p, ",")
		la, _ = strconv.ParseFloat(parts[0], 64)
		ln, _ = strconv.ParseFloat(parts[1], 64)
		if haversineKm(la, ln, 35.0, -90.0) > 30 {
			t.Errorf("weather point %q is not around the fix", p)
		}
	}
	if tt.route.Load() != 0 || tt.geo.Load() != 0 || tt.along.Load() != 0 || tt.near.Load() != 0 {
		t.Fatal("a plain Aimless HUD refresh must not route, geocode or search")
	}
	if nofix := hudAt(t, app, c, ""); nofix.Free == nil || nofix.Free.State != "aimless" || nofix.Free.CanPicks || nofix.NextName != "" {
		t.Fatalf("without a fix: %+v", nofix.Free)
	}
}

func TestAimlessMilesAddUpBetweenFixesAndResetOnAModeChange(t *testing.T) {
	tt := newTT(t)
	app, c := freeApp(t, tt)
	goAimless(t, app, c)
	miles := func(at string) string { return hudAt(t, app, c, at).Free.Miles }
	if m := miles("?lat=35.0000&lng=-90.0000"); m != "0 mi" {
		t.Fatalf("first fix: %q", m)
	}
	if m := miles("?lat=35.1000&lng=-90.0000"); m != "7 mi" {
		t.Fatalf("0.1 degree north is about 7 miles: %q", m)
	}
	if m := miles("?lat=35.1000&lng=-90.0000"); m != "7 mi" {
		t.Fatalf("the same fix again adds nothing: %q", m)
	}
	if m := miles("?lat=35.2000&lng=-90.0000"); m != "14 mi" {
		t.Fatalf("two legs: %q", m)
	}
	doAs(t, app, c, "POST", "/driver/freelance/mode", url.Values{"mode": {"trip"}})
	goAimless(t, app, c)
	if m := miles("?lat=35.3000&lng=-90.0000"); m != "0 mi" {
		t.Fatalf("a mode change starts a new session: %q", m)
	}
	if m := miles("?lat=35.4000&lng=-90.0000"); m != "7 mi" {
		t.Fatalf("after the reset: %q", m)
	}
}

func TestAimlessFindPicksNearbyAsksTomTomAroundTheFixAndNotAlongARoute(t *testing.T) {
	tt := newTT(t)
	app, c := aimlessApp(t, tt)
	o := findPicksAt(t, app, c, "35.0000", "-90.0000")
	if o.State != "ok" || tt.near.Load() == 0 {
		t.Fatalf("a fix far from private places must be searched: %+v near=%d", o, tt.near.Load())
	}
	if tt.along.Load() != 0 || tt.route.Load() != 0 || tt.geo.Load() != 0 {
		t.Fatal("Aimless has no route: only the nearby search is asked")
	}
	tt.mu.Lock()
	for _, r := range tt.nearReq {
		if r["lat"] != "35.0000" || r["lon"] != "-90.0000" || r["radius"] == "" {
			t.Errorf("nearby search was sent %v, want the fix and a radius", r)
		}
	}
	tt.mu.Unlock()
	var coffee *freeCatOut
	for i := range o.Cats {
		if o.Cats[i].Key == "coffee" {
			coffee = &o.Cats[i]
		}
	}
	if coffee == nil || coffee.State != "items" || len(coffee.Picks) != 2 || coffee.Picks[0].Name != "Corner Roasters" ||
		!strings.Contains(coffee.Picks[0].Detour, "away") {
		t.Fatalf("coffee, nearest first with a distance: %+v", coffee)
	}
	if got := catKeys(o); strings.Contains(got, "evening") {
		t.Fatalf("no evening category without a route to the end of a day: %s", got)
	}
}

func TestAimlessFindPicksFailsClosedNearAPrivatePlace(t *testing.T) {
	for _, fix := range [][2]string{{"40.1000", "-100.4500"}, {"40.1500", "-100.4000"}, {"44.3000", "-106.3000"}} {
		tt := newTT(t)
		app, c := aimlessApp(t, tt)
		o := findPicksAt(t, app, c, fix[0], fix[1])
		if tt.near.Load() != 0 || tt.along.Load() != 0 {
			t.Fatalf("fix %v: %d TomTom searches were made from inside a private radius", fix, tt.near.Load())
		}
		if o.State != "route_unavailable" || len(o.Cats) != 0 || o.Text == "" {
			t.Fatalf("fix %v: want the fail-closed state, got %+v", fix, o)
		}
	}
}

func TestAimlessFindPicksNeedsAFixAndTheModeItself(t *testing.T) {
	tt := newTT(t)
	app, c := aimlessApp(t, tt)
	w := doAs(t, app, c, "POST", "/driver/freelance/picks", url.Values{})
	var o freePicksOut
	if json.Unmarshal(w.Body.Bytes(), &o) != nil || o.State != "no_fix" || tt.near.Load() != 0 {
		t.Fatalf("no fix: %+v near=%d", o, tt.near.Load())
	}
	other := asUser(t, app, "lars", roleAdmin)
	w = doAs(t, app, other, "POST", "/driver/freelance/picks", url.Values{"lat": {"35.0"}, "lng": {"-90.0"}})
	if json.Unmarshal(w.Body.Bytes(), &o) != nil || o.State != "need_dest" || tt.near.Load() != 0 {
		t.Fatalf("a user not in Aimless must not search: %+v", o)
	}
}

func TestAimlessLeavesThePlannedRideUntouched(t *testing.T) {
	tt := newTT(t)
	app, c := aimlessApp(t, tt)
	snap := func() string {
		rv, err := app.loadRide()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(rv)
		return string(b)
	}
	before := snap()
	for _, at := range []string{"?lat=35.0000&lng=-90.0000", "?lat=35.1000&lng=-90.0000", ""} {
		hudAt(t, app, c, at)
	}
	findPicksAt(t, app, c, "35.0000", "-90.0000")
	doAs(t, app, c, "GET", "/driver", nil)
	if after := snap(); after != before {
		t.Fatal("an Aimless session changed the planned ride")
	}
}

func TestAimlessPicksAreLoggedAsNearbyPicksUnderTheNewPromptVersion(t *testing.T) {
	tt := newTT(t)
	app, c := aimlessApp(t, tt)
	stub := newAPIStub(t, messageJSON(`{"picks":[{"id":"coffee-1","note":"Small roaster."}]}`))
	app.chester = newChester(chesterEnv(t, stub, nil))
	if o := findPicksAt(t, app, c, "35.0000", "-90.0000"); o.State != "ok" {
		t.Fatalf("picks: %+v", o)
	}
	rows := chesterRows(t, app)
	if len(rows) != 1 || !strings.HasPrefix(rows[0].Question, "nearby picks: ") || rows[0].PromptVersion != "freelance-picks-4" {
		t.Fatalf("an Aimless picks call is logged as nearby picks under freelance-picks-4: %+v", rows)
	}
	if raw := string(stub.requests()[0].Body); !strings.Contains(raw, "near the car when there is no route") {
		t.Fatal("the picks prompt must say it also serves a drive with no route")
	}
}
