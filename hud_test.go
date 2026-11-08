package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func hudRide(t *testing.T) (*App, *rideView) {
	t.Helper()
	app := testApp(t)
	rv, err := app.loadRide()
	if err != nil {
		t.Fatal(err)
	}
	return app, rv
}

// midVertex is a point on a leg's own line, so a position "on the road".
func midVertex(t *testing.T, l Leg) (float64, float64) {
	t.Helper()
	var pts [][]float64
	if err := json.Unmarshal(l.Line, &pts); err != nil || len(pts) < 3 {
		t.Fatalf("leg line: %v", err)
	}
	p := pts[len(pts)/2]
	return p[1], p[0]
}

func TestHUDFallsBackToThePlanByDate(t *testing.T) {
	_, rv := hudRide(t)
	// Day 2's date, noon local at the leg's start.
	leg := rv.Legs[1]
	loc := loadLoc(leg.TZ)
	d, _ := time.ParseInLocation("2006-01-02", leg.Date, loc)
	now := d.Add(12 * time.Hour)
	h := buildHUD(rv.Ride, rv.Legs, now, nil, nil, false)
	if h.Source != "plan" || h.DayN != 2 || h.LegSlug != leg.Slug || h.DayTotal != len(rv.Legs) {
		t.Fatalf("plan fallback: %+v", h)
	}
	if !h.HasNext || h.Waze == "" || !strings.HasPrefix(h.Waze, "https://waze.com/ul?") {
		t.Fatalf("a next stop with a Waze link is the centre of the screen: %+v", h)
	}
	if !strings.HasPrefix(h.Google, "https://www.google.com/maps/dir/?api=1&destination=") || !strings.HasPrefix(h.Apple, "https://maps.apple.com/?daddr=") {
		t.Fatalf("every stop gets Google and Apple links beside Waze: %q %q", h.Google, h.Apple)
	}
	if h.TonightName == "" || h.TZ == "" {
		t.Fatalf("tonight's stop and the zone are gauges: %+v", h)
	}
}

func TestHUDPositionPicksTheLegAndSplitsMiles(t *testing.T) {
	_, rv := hudRide(t)
	leg := rv.Legs[2]
	lat, lng := midVertex(t, leg)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) // the date points elsewhere
	h := buildHUD(rv.Ride, rv.Legs, now, &lat, &lng, false)
	if h.Source != "position" || h.LegSlug != leg.Slug {
		t.Fatalf("a fix on leg 3's line must pick leg 3: %+v", h)
	}
	if !h.HaveMiles || h.MilesDoneN <= 0 || h.MilesLeftN <= 0 {
		t.Fatalf("mid-leg is some miles done and some left: %+v", h)
	}
	// A fix nowhere near the line falls back to the plan and claims no miles.
	far, farLng := 0.0, 0.0
	h = buildHUD(rv.Ride, rv.Legs, now, &far, &farLng, false)
	if h.Source != "plan" {
		t.Fatalf("off-route fix must not be believed: %+v", h)
	}
}

func TestHUDNoMilesIsDashNotZero(t *testing.T) {
	_, rv := hudRide(t)
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) // before the ride
	h := buildHUD(rv.Ride, rv.Legs, now, nil, nil, false)
	if h.HaveMiles || h.MilesDone != "—" {
		t.Fatalf("without a fix or an arrival the done figure is unknown, not 0: %+v", h)
	}
}

func TestHUDCallsOutAZoneChange(t *testing.T) {
	_, rv := hudRide(t)
	// Find a leg whose checkpoints do not all share one clock at that date.
	found := false
	for _, l := range rv.Legs {
		d, _ := time.ParseInLocation("2006-01-02", l.Date, loadLoc(l.TZ))
		h := buildHUD(rv.Ride, rv.Legs, d.Add(12*time.Hour), nil, nil, false)
		if h.Crossing != "" {
			found = true
			if !strings.Contains(h.Crossing, "→") {
				t.Fatalf("callout should name both clocks: %q", h.Crossing)
			}
		}
	}
	if !found {
		t.Fatal("the Austin to San Jose seed crosses zones; no leg called it out")
	}
}

func TestDriverHomeIsTheHUDAndStaysBehindSignIn(t *testing.T) {
	app, _ := hudRide(t)
	if w := doAs(t, app, nil, "GET", "/driver", nil); w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/driver/login") {
		t.Fatalf("a visitor must be sent to sign in: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := doAs(t, app, nil, "GET", "/driver/hud.json", nil); w.Code != http.StatusFound {
		t.Fatalf("hud.json is signed-in only: %d", w.Code)
	}
	c := asUser(t, app, "kris", roleAdmin)
	w := doAs(t, app, c, "GET", "/driver", nil)
	if w.Code != 200 {
		t.Fatalf("/driver: %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`class="hud`, `href="/driver/post"`, `href="/checklist"`, `href="/plan"`, "Navigate in Waze", `id="hud-gps"`, `id="hud-net"`, `id="hud-sync"`, "Weather: checking"} {
		if !strings.Contains(body, want) {
			t.Errorf("HUD lacks %q", want)
		}
	}
	// Chester is off in tests: its tool is absent, not dead.
	if strings.Contains(body, "Ask Chester") {
		t.Error("Ask Chester shown while Chester is off")
	}
	// No upload queue exists, so no queue count may be invented.
	if strings.Contains(strings.ToLower(body), "queued") {
		t.Error("the HUD shows a queue that does not exist")
	}
}

func TestHUDJSONCarriesHazardsFromTheStubs(t *testing.T) {
	app, _ := hudRide(t)
	nws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"features":[{"properties":{"id":"x","event":"Red Flag Warning","areaDesc":"Here"}}]}`))
	}))
	defer nws.Close()
	app.hazards = hazardSvcFor(nws, nil, "") // no key: traffic must say so
	c := asUser(t, app, "kris", roleAdmin)
	w := doAs(t, app, c, "GET", "/driver/hud.json", nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var out struct {
		Hazards []hazardChip `json:"hazards"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	txt := chipTexts(out.Hazards)
	if !strings.Contains(txt, "Red Flag Warning") || !strings.Contains(txt, "Traffic: unknown (no key)") {
		t.Fatalf("hazards:\n%s", txt)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("a signed-in answer must not be cached: %q", cc)
	}
}

func TestHUDJSONTripTrafficIsBudgetedByTheLegLineAndTheFix(t *testing.T) {
	app, rv := hudRide(t)
	leg := rv.Legs[2]
	lat, lng := midVertex(t, leg)
	var pts [][]float64
	if err := json.Unmarshal(leg.Line, &pts); err != nil {
		t.Fatal(err)
	}
	end := pts[len(pts)-1]
	var gotQ atomic.Value
	tt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ.Store(r.URL.Query().Get("fields"))
		w.Write([]byte(`{"incidents":[
 {"geometry":{"type":"Point","coordinates":[` + strconv.FormatFloat(end[0], 'f', 6, 64) + `,` + strconv.FormatFloat(end[1], 'f', 6, 64) + `]},"properties":{"id":"on","iconCategory":8,"magnitudeOfDelay":4,"events":[{"description":"Road closed"}],"from":"Test Rd","to":"Spur Rd"}},
 {"geometry":{"type":"Point","coordinates":[10.0,10.0]},"properties":{"id":"far","iconCategory":8,"magnitudeOfDelay":4,"events":[{"description":"Road closed"}],"from":"Far Rd","to":"Away Rd"}}]}`))
	}))
	defer tt.Close()
	nws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"features":[]}`)) }))
	defer nws.Close()
	app.hazards = hazardSvcFor(nws, tt, "k-test")
	c := asUser(t, app, "kris", roleAdmin)
	h := hudAt(t, app, c, fmt.Sprintf("?lat=%f&lng=%f", lat, lng))
	if q, _ := gotQ.Load().(string); !strings.Contains(q, "geometry") {
		t.Fatalf("the trip HUD must ask TomTom for geometry: %q", q)
	}
	txt := chipTexts(h.Hazards)
	if !strings.Contains(txt, "mi ahead, Road closed, Test Rd to Spur Rd") || strings.Contains(txt, "Far Rd") {
		t.Fatalf("want the on-route closure with a distance and not the far one:\n%s", txt)
	}
}
