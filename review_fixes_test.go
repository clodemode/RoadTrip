package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The fixes a cold review of the trip branch asked for. Every fixture here is
// the synthetic one in testdata/, or built from it in the test.

// browserPost is a form submitted by a page: the token for a credential, and
// the Accept header a browser sends, which is what asks for a redirect back
// to the page instead of a JSON body.
func browserPost(t *testing.T, app *App, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, req)
	return w
}

// --- 1. a leg name is text, wherever it lands ----------------------------------------

// markupName is a leg name that is markup. If any surface hands it to a
// browser unescaped, the browser runs it.
const markupName = `Cedar <img src=x onerror=alert(1)> Dogwood`

func TestLegNameWithMarkupIsDataNotMarkup(t *testing.T) {
	app := tokenApp(t, testToken)
	doc := bytes.Replace(fixtureTrip(t), []byte(`Cedar Camp → Dogwood`), []byte(markupName), 1)
	if !bytes.Contains(doc, []byte(markupName)) {
		t.Fatal("the fixture has no day-2 leg name to replace; this test would pass on nothing")
	}
	if w := pushTrip(t, app, doc, "?activate=1"); w.Code != http.StatusOK {
		t.Fatalf("push: status %d: %s", w.Code, w.Body.String())
	}

	// The name reaches the page's script as DATA, whole: /plan.json is the
	// same document the page embeds as window.PLAN.
	b := getPlanJSON(t, app, "")
	if len(b.Plan.Days) < 2 || b.Plan.Days[1].Name != markupName {
		t.Fatalf("/plan.json does not carry the leg name as written: %+v", b.Plan.Days)
	}

	// No page inlines it as markup. The server-rendered day list escapes it,
	// and the embedded JSON writes < as a backslash-u escape, so the text cannot close the
	// script element it sits in either.
	for _, p := range []string{"/plan", "/", "/legs/day-2"} {
		w := send(t, app, "GET", p, "", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", p, w.Code)
		}
		body := w.Body.String()
		if strings.Contains(body, "<img src=x") {
			t.Errorf("%s inlines the leg name as markup", p)
		}
		// Positive control: the name IS on the page, in its escaped forms.
		if !strings.Contains(body, "&lt;img src=x") && !strings.Contains(body, `\u003cimg src=x`) {
			t.Errorf("%s does not carry the leg name at all; the check above passed on nothing", p)
		}
	}
	plan := send(t, app, "GET", "/plan", "", "", nil).Body.String()
	if !strings.Contains(plan, `\u003cimg src=x onerror=alert(1)\u003e`) {
		i := strings.LastIndex(plan, "onerror")
		t.Errorf("window.PLAN does not carry the leg name as escaped JSON: %q", plan[max(0, i-60):min(len(plan), i+60)])
	}

	// The last step is in the browser: plan.js hands the name to Leaflet, which
	// renders a string tooltip as HTML. There is no JavaScript test harness in
	// this repository, so that path is not executed here. What this pins is
	// the source: the tooltip escapes the name, and no line concatenates a raw
	// leg name into something Leaflet or innerHTML will parse.
	src, err := fs.ReadFile(staticFS, "static/js/plan.js")
	if err != nil {
		t.Fatalf("read plan.js: %v", err)
	}
	js := string(src)
	if !strings.Contains(js, "bindTooltip(") || !strings.Contains(js, "esc(x.leg.name)") {
		t.Error("plan.js binds the leg tooltip without escaping the leg name")
	}
	for _, raw := range []string{"+ x.leg.name", "+ c.name", "+ c.label", "+ d.name"} {
		if strings.Contains(js, raw) {
			t.Errorf("plan.js concatenates a document string unescaped: %q", raw)
		}
	}
}

// --- 2. the saved start date wins, and the push says so ------------------------------

type pushBody struct {
	Status          string    `json:"status"`
	StartDate       string    `json:"start_date"`
	DocStartDate    string    `json:"document_start_date"`
	StartNotApplied string    `json:"start_date_not_applied"`
	Selection       Selection `json:"selection"`
}

func pushBodyOf(t *testing.T, w *httptest.ResponseRecorder) pushBody {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("push: status %d: %s", w.Code, w.Body.String())
	}
	var b pushBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("push body: %v", err)
	}
	return b
}

func TestRePushReportsTheStartDateInForce(t *testing.T) {
	app := tokenApp(t, testToken)
	// The first push: there is no saved selection, so the document's start is
	// the start, and there is nothing to report.
	first := pushBodyOf(t, pushTrip(t, app, fixtureTrip(t), "?activate=1"))
	if first.StartDate != "2030-05-01" || first.StartNotApplied != "" || first.DocStartDate != "" {
		t.Fatalf("first push: %+v", first)
	}

	later := bytes.Replace(fixtureTrip(t), []byte(`"start_date": "2030-05-01"`), []byte(`"start_date": "2030-06-10"`), 1)
	if bytes.Equal(later, fixtureTrip(t)) {
		t.Fatal("the fixture has no start_date to move; this test would pass on nothing")
	}
	// The re-push answers ok, and the ride does not move: the rule.
	re := pushBodyOf(t, pushTrip(t, app, later, ""))
	if re.Status != "ok" || re.StartDate != "2030-05-01" || re.Selection.StartDate != "2030-05-01" {
		t.Errorf("re-push: the start in force should still be the saved one: %+v", re)
	}
	// And it is loud about it.
	if re.DocStartDate != "2030-06-10" {
		t.Errorf("re-push: document_start_date %q, want the document's own", re.DocStartDate)
	}
	for _, want := range []string{"not applied", "POST /driver/plan"} {
		if !strings.Contains(re.StartNotApplied, want) {
			t.Errorf("re-push: start_date_not_applied %q lacks %q", re.StartNotApplied, want)
		}
	}
	if ride, _ := app.store.Ride(fixtureSlug); ride.StartDate != "2030-05-01" {
		t.Errorf("the re-pushed document moved the start to %s", ride.StartDate)
	}
	if legs := mustLegs(t, app, fixtureSlug); legs[0].Date != "2030-05-01" {
		t.Errorf("the re-pushed document re-dated leg 1 to %s", legs[0].Date)
	}

	// Boot reads the same file under the same rule, and says so without a date.
	logged := captureLog(t)
	if loaded, failed := loadVolumeTrips(app.store, app.dataDir); loaded != 1 || failed != 0 {
		t.Fatalf("boot pass: %d loaded, %d failed", loaded, failed)
	}
	if !strings.Contains(logged.String(), "start date was not applied") {
		t.Errorf("the boot pass was silent about the start date: %s", logged.String())
	}
	if strings.Contains(logged.String(), "2030-") {
		t.Errorf("a log line carries a date: %s", logged.String())
	}

	// The start moves through /driver/plan. Once it has, the same document
	// agrees with what is saved and the push has nothing to report.
	if w := postForm(t, app, "/driver/plan", url.Values{"start": {"2030-06-10"}}); w.Code != http.StatusOK {
		t.Fatalf("moving the start: %d %s", w.Code, w.Body.String())
	}
	agreed := pushBodyOf(t, pushTrip(t, app, later, ""))
	if agreed.StartDate != "2030-06-10" || agreed.StartNotApplied != "" || agreed.DocStartDate != "" {
		t.Errorf("a document that agrees with the saved start still reports a difference: %+v", agreed)
	}
}

// --- 3. a leg line has to be where its leg is ----------------------------------------

// shiftedFixture is the fixture trip moved 20° east. The fixture sits near
// longitude -100, where a swapped point is already out of range as a
// latitude; the defect lives at longitudes inside ±90, where a swapped point
// is a perfectly good place on the wrong side of the planet. swapLeg, when
// not empty, writes that leg's line [lat, lng].
func shiftedFixture(t *testing.T, swapLeg string) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(fixtureTrip(t), &doc); err != nil {
		t.Fatal(err)
	}
	swapped := 0
	for _, f := range doc["features"].([]any) {
		feat := f.(map[string]any)
		geom := feat["geometry"].(map[string]any)
		props := feat["properties"].(map[string]any)
		switch geom["type"] {
		case "Point":
			xy := geom["coordinates"].([]any)
			xy[0] = xy[0].(float64) + 20
		case "LineString":
			swap := swapLeg != "" && props["slug"] == swapLeg
			for _, p := range geom["coordinates"].([]any) {
				xy := p.([]any)
				xy[0] = xy[0].(float64) + 20
				if swap {
					xy[0], xy[1] = xy[1], xy[0]
				}
			}
			if swap {
				swapped++
			}
		}
	}
	if swapLeg != "" && swapped != 1 {
		t.Fatalf("no leg %q to swap; this test would pass on nothing", swapLeg)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// lineNumbers is every coordinate of one leg's line as the document writes
// it: what a refusal must not repeat.
func lineNumbers(t *testing.T, doc []byte, slug string) []string {
	t.Helper()
	var d struct {
		Features []struct {
			Properties map[string]any `json:"properties"`
			Geometry   struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range d.Features {
		if f.Geometry.Type != "LineString" || f.Properties["slug"] != slug {
			continue
		}
		var pts [][]float64
		if err := json.Unmarshal(f.Geometry.Coordinates, &pts); err != nil {
			t.Fatal(err)
		}
		for _, p := range pts {
			for _, v := range p {
				// Only a number with a fraction: a whole one could match a
				// clock reading in a log line and prove nothing.
				if n := strconv.FormatFloat(v, 'f', -1, 64); strings.Contains(n, ".") {
					out = append(out, n)
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("no line for leg %q", slug)
	}
	return out
}

func TestSwappedLegLineIsRefused(t *testing.T) {
	app := tokenApp(t, testToken)
	before, _ := app.store.Rides()

	// A fixed leg, and a leg that only one choice of an option takes: the
	// check runs under every choice, not only the defaults.
	for slug, wants := range map[string][]string{
		"day-2":        {"leg 2"},
		"day-1-valley": {"leg 1", `day1=valley`},
	} {
		bad := shiftedFixture(t, slug)
		w := pushTrip(t, app, bad, "?activate=1")
		body := w.Body.String()
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s swapped: status %d, want 400: %s", slug, w.Code, body)
		}
		for _, want := range append(wants, "[lng, lat]", strconv.Itoa(maxLineEndKm)+" km") {
			if !strings.Contains(body, want) {
				t.Errorf("%s swapped: the refusal lacks %q: %s", slug, want, body)
			}
		}
		// It names the leg. It does not repeat where anything is.
		for _, n := range lineNumbers(t, bad, slug) {
			if strings.Contains(body, n) {
				t.Errorf("%s swapped: the refusal echoes a coordinate", slug)
				break
			}
		}
	}
	if files := tripFiles(t, app); len(files) != 0 {
		t.Errorf("a refused push wrote %v", files)
	}
	if after, _ := app.store.Rides(); len(after) != len(before) {
		t.Errorf("a refused push changed the rides: %d -> %d", len(before), len(after))
	}

	// The same document on the volume is skipped at boot, not loaded.
	dir := filepath.Join(app.dataDir, tripsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bad := shiftedFixture(t, "day-2")
	if err := os.WriteFile(filepath.Join(dir, fixtureSlug+".geojson"), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	logged := captureLog(t)
	if loaded, failed := loadVolumeTrips(app.store, app.dataDir); loaded != 0 || failed != 1 {
		t.Errorf("boot pass on a swapped line: %d loaded, %d failed; want 0 and 1", loaded, failed)
	}
	if !strings.Contains(logged.String(), "leg 2") {
		t.Errorf("the boot log does not name the leg: %s", logged.String())
	}
	for _, n := range lineNumbers(t, bad, "day-2") {
		if strings.Contains(logged.String(), n) {
			t.Error("the boot log echoes a coordinate")
			break
		}
	}
	if _, err := app.store.Ride(fixtureSlug); err == nil {
		t.Error("a swapped line reached the database from the volume")
	}
	if err := os.Remove(filepath.Join(dir, fixtureSlug+".geojson")); err != nil {
		t.Fatal(err)
	}

	// Positive control: the same shifted document with no line swapped is a
	// good trip. Without this, "refused" above could be the shift's doing.
	if w := pushTrip(t, app, shiftedFixture(t, ""), "?activate=1"); w.Code != http.StatusOK {
		t.Fatalf("the shifted fixture, unswapped: status %d: %s", w.Code, w.Body.String())
	}
	// And a saved selection is held to it as well: it re-reads the same file.
	if w := postForm(t, app, "/driver/plan", url.Values{"opt-day1": {"valley"}}); w.Code != http.StatusOK {
		t.Errorf("a selection on a good trip: %d %s", w.Code, w.Body.String())
	}
}

// lineSeed is a one-leg plan with a checkpoint at each end, the line's last
// point offKm north of the finish checkpoint.
func lineSeed(offKm float64) *seedData {
	const kmPerDegLat = 111.195
	endLat := 40 + offKm/kmPerDegLat
	line := fmt.Sprintf(`{"type":"LineString","coordinates":[[-80,40],[-79.5,40],[-79,%.6f]]}`, endLat)
	return &seedData{
		Legs: []seedLeg{{Ord: 1, Day: 1, Slug: "leg-1", Name: "one leg", Line: line}},
		Checkpoints: []seedCP{
			{LegOrd: 1, Ord: 1, Name: "start", Lat: 40, Lng: -80, TZ: "UTC"},
			{LegOrd: 1, Ord: 2, Name: "finish", Lat: 40, Lng: -79, TZ: "UTC", IsTerminal: true},
		},
	}
}

func TestLineEndThreshold(t *testing.T) {
	if maxLineEndKm != 50 {
		t.Fatalf("the threshold is %d km; this test and README.md state 50", maxLineEndKm)
	}
	for _, c := range []struct {
		off  float64
		pass bool
	}{{0, true}, {40, true}, {49, true}, {51, false}, {60, false}, {500, false}} {
		err := checkPlan(lineSeed(c.off))
		if c.pass && err != nil {
			t.Errorf("a line ending %.0f km from its checkpoint was refused: %v", c.off, err)
		}
		if !c.pass && (err == nil || !strings.Contains(err.Error(), "leg 1") || !strings.Contains(err.Error(), "ends more than")) {
			t.Errorf("a line ending %.0f km from its checkpoint: %v", c.off, err)
		}
	}
	// A leg with no checkpoint at either end cannot be placed, so it is not
	// passed: what the check cannot see, it refuses.
	sd := lineSeed(0)
	sd.Legs = append([]seedLeg{{Ord: 1, Day: 1, Slug: "leg-0", Name: "unplaced",
		Line: `{"type":"LineString","coordinates":[[-82,40],[-80,40]]}`}}, sd.Legs...)
	sd.Legs[1].Ord = 2
	sd.Checkpoints = sd.Checkpoints[1:]
	sd.Checkpoints[0].LegOrd, sd.Checkpoints[0].Ord = 2, 1
	if err := checkPlan(sd); err == nil || !strings.Contains(err.Error(), "leg 1 has no checkpoint at either end") {
		t.Errorf("a leg nothing can place: %v", err)
	}
}

// The embedded seed is coarse — a dozen points a leg — and it is inside the
// check all the same: every one of its lines ends on its checkpoint. Boot
// does not run checkPlan on an embedded seed; this is what would say so if a
// future seed edit could not pass it.
func TestEmbeddedSeedPassesTheLineCheck(t *testing.T) {
	names, err := fs.Glob(seedFS, "seed/*.geojson")
	if err != nil || len(names) == 0 {
		t.Fatalf("no embedded seed: %v", err)
	}
	for _, name := range names {
		raw, err := seedFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
		sd, err := parseSeedBytes(raw, base, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(sd.Legs) < 2 || len(sd.Checkpoints) < 2 {
			t.Fatalf("%s: %d legs, %d checkpoints; the check would pass on nothing", name, len(sd.Legs), len(sd.Checkpoints))
		}
		if err := checkPlan(sd); err != nil {
			t.Errorf("%s does not pass the line check: %v", name, err)
		}
	}
	// And the synthetic fixture, under every choice (checkTrip walks them).
	if _, err := checkTrip(fixtureTrip(t)); err != nil {
		t.Errorf("the fixture trip: %v", err)
	}
}

// --- 4. ?error= prints a fixed sentence or nothing -----------------------------------

func TestErrorQueryPrintsOnlyFixedSentences(t *testing.T) {
	app := checklistApp(t)
	free := []string{
		"Free cake at example.test",
		"<script>alert(1)</script>",
		"conflict zzqx-more", // a known code is matched whole, not as a prefix
		"CONFLICT",
	}
	for _, page := range []string{"/plan", "/checklist"} {
		for _, token := range []string{"", testToken} {
			if token == "" && page == "/checklist" {
				continue // driver-only: a visitor is redirected, with the query in the next= link
			}
			for _, text := range free {
				body := send(t, app, "GET", page+"?error="+url.QueryEscape(text)+"&ok="+url.QueryEscape(text), token, "", nil).Body.String()
				for _, leak := range []string{"Free cake", "example.test", "alert(1)", "zzqx-more", "CONFLICT", "Not saved", "Saved."} {
					if strings.Contains(body, leak) {
						t.Errorf("%s?error=%q (signed in: %v) prints %q", page, text, token != "", leak)
					}
				}
			}
		}
	}
	// Positive control, and the whole table: every code prints its sentence.
	if len(planRefusals) < 5 || len(checklistRefusals) < 5 {
		t.Fatal("the code tables are nearly empty; the checks above would pass on nothing")
	}
	for page, table := range map[string]map[string]string{"/plan": planRefusals, "/checklist": checklistRefusals} {
		for code, sentence := range table {
			body := send(t, app, "GET", page+"?error="+url.QueryEscape(code), testToken, "", nil).Body.String()
			if want := "Not saved: " + template.HTMLEscapeString(sentence) + "."; !strings.Contains(body, want) {
				t.Errorf("%s?error=%s does not print %q", page, code, want)
			}
		}
	}

	// What the app itself puts in a redirect is a code from the table: the
	// reason in words stays out of the address.
	finish := cpByOrd(t, app, 5)
	if err := app.store.SetArrived(finish.ID, parseUTCMust(t, "2030-05-03T22:00:00Z"), nil); err != nil {
		t.Fatal(err)
	}
	redirects := map[string]*httptest.ResponseRecorder{
		"/plan": browserPost(t, app, "/driver/plan", url.Values{"opt-finish": {"stopover"}}),
		"/plan (a choice the trip does not have)":   browserPost(t, app, "/driver/plan", url.Values{"opt-day1": {"tunnel"}}),
		"/plan (a start that is not a date)":        browserPost(t, app, "/driver/plan", url.Values{"start": {"the 2nd"}}),
		"/checklist (a public note that is a VIN)":  browserPost(t, app, "/driver/checklist/step/inspect", url.Values{"outcome": {fixtureVIN}}),
		"/checklist (a time that has not happened)": browserPost(t, app, "/driver/checklist/step/inspect", url.Values{"at": {"2999-01-01T00:00"}}),
	}
	wantCode := map[string]string{
		"/plan": planErrConflict,
		"/plan (a choice the trip does not have)":   planErrChoice,
		"/plan (a start that is not a date)":        planErrStart,
		"/checklist (a public note that is a VIN)":  clErrVIN,
		"/checklist (a time that has not happened)": clErrFuture,
	}
	for name, rec := range redirects {
		loc, err := url.Parse(rec.Header().Get("Location"))
		if rec.Code != http.StatusSeeOther || err != nil {
			t.Errorf("%s: status %d, Location %q", name, rec.Code, rec.Header().Get("Location"))
			continue
		}
		if got := loc.Query().Get("error"); got != wantCode[name] {
			t.Errorf("%s: the redirect carries error=%q, want the code %q", name, got, wantCode[name])
		}
		if strings.ContainsAny(loc.Query().Get("error"), " %+") {
			t.Errorf("%s: the redirect carries words: %q", name, loc.RawQuery)
		}
	}
}

func parseUTCMust(t *testing.T, s string) time.Time {
	t.Helper()
	v, ok := parseUTC(s)
	if !ok {
		t.Fatalf("not a UTC stamp: %s", s)
	}
	return v
}

// --- 5. a signed-in page is never cached ---------------------------------------------

func TestSignedInPagesAreNeverCached(t *testing.T) {
	app := privateTextApp(t)
	rec := httptest.NewRecorder()
	app.auth.Issue(rec, false, testAdminUser(t, app))
	cookie := rec.Result().Cookies()[0]

	for _, p := range []string{"/checklist", "/", "/plan", "/photos"} {
		// Anonymous: exactly as it was. No cache header and no Vary.
		anon := send(t, app, "GET", p, "", "", nil)
		wantAnon := http.StatusOK
		if p == "/checklist" {
			wantAnon = http.StatusFound // driver-only
		}
		if anon.Code != wantAnon {
			t.Fatalf("anonymous %s: status %d, want %d", p, anon.Code, wantAnon)
		}
		if cc, vary := anon.Header().Get("Cache-Control"), anon.Header().Values("Vary"); cc != "" || len(vary) != 0 {
			t.Errorf("anonymous %s: Cache-Control %q, Vary %v; the anonymous response must not change", p, cc, vary)
		}
		if ct := anon.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("anonymous %s: Content-Type %q", p, ct)
		}

		// Signed in, by the token and by the cookie.
		byToken := send(t, app, "GET", p, testToken, "", nil)
		req := httptest.NewRequest("GET", p, nil)
		req.AddCookie(cookie)
		byCookie := httptest.NewRecorder()
		app.routes().ServeHTTP(byCookie, req)
		for how, w := range map[string]*httptest.ResponseRecorder{"token": byToken, "cookie": byCookie} {
			if w.Code != http.StatusOK {
				t.Fatalf("%s by %s: status %d", p, how, w.Code)
			}
			if cc := w.Header().Get("Cache-Control"); cc != "private, no-store" {
				t.Errorf("%s by %s: Cache-Control %q, want \"private, no-store\"", p, how, cc)
			}
			if vary := w.Header().Values("Vary"); len(vary) != 1 || vary[0] != "Cookie" {
				t.Errorf("%s by %s: Vary %v, want [Cookie]", p, how, vary)
			}
		}
		// Positive control: the signed-in /checklist really is the page with
		// private text on it, the thing a cache must not keep.
		if p == "/checklist" {
			if !strings.Contains(byToken.Body.String(), privateMark) || !strings.Contains(byCookie.Body.String(), privateMark) {
				t.Error("the signed-in /checklist carries no private text; the header check is about nothing")
			}
			if strings.Contains(anon.Body.String(), privateMark) {
				t.Error("the anonymous /checklist carries private text")
			}
		}
	}
	// A signed-in admin page takes the same headers from the same place.
	if w := send(t, app, "GET", "/driver/lodging", testToken, "", nil); w.Code != http.StatusOK ||
		w.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("/driver/lodging: status %d, Cache-Control %q", w.Code, w.Header().Get("Cache-Control"))
	}
	// The sign-in form is an anonymous page and stays one.
	if w := send(t, app, "GET", "/driver/login", "", "", nil); w.Header().Get("Cache-Control") != "" {
		t.Errorf("/driver/login: Cache-Control %q on an anonymous page", w.Header().Get("Cache-Control"))
	}
}

// --- 6. a save answers in place -------------------------------------------------------

func TestPlanSaveAnswersInPlace(t *testing.T) {
	app := activeTripApp(t)

	// A good save: back to the line under the button, with a code.
	rec := browserPost(t, app, "/driver/plan", url.Values{"opt-day1": {"valley"}})
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/plan?ok=saved#plan-result" {
		t.Fatalf("a saved plan: status %d, Location %q", rec.Code, loc)
	}
	if sel := savedSelection(t, app); sel.Choices["day1"] != "valley" {
		t.Fatalf("the save did not land: %+v", sel)
	}
	saved := send(t, app, "GET", "/plan?ok=saved", testToken, "", nil).Body.String()
	const line = `<p class="plan-saved plan-msg" id="plan-result" role="status">Saved.</p>`
	if !strings.Contains(saved, line) {
		t.Error("the page does not say Saved.")
	}
	// One line, in place: after the button, inside the form.
	form := strings.Index(saved, `<form id="plan-form"`)
	if form < 0 {
		t.Fatal("the page has no plan form")
	}
	button, at := strings.Index(saved, "Save this plan"), strings.Index(saved, line)
	end := form + strings.Index(saved[form:], "</form>")
	if button < form || at < button || end < at {
		t.Errorf("Saved. is not under the Save button inside the form (form %d, button %d, line %d, form end %d)", form, button, at, end)
	}
	if n := strings.Count(saved, `id="plan-result"`); n != 1 {
		t.Errorf("%d result lines, want one", n)
	}
	if strings.Contains(saved, "Not saved") {
		t.Error("a saved plan also says Not saved")
	}
	// No modal and no script alert.
	for _, banned := range []string{"<dialog", "alert(", "confirm("} {
		if strings.Contains(saved, banned) {
			t.Errorf("the saved page uses %q", banned)
		}
	}
	// Without the code the page says nothing; with another code, nothing.
	for _, q := range []string{"", "?ok=1", "?ok=Saved.", "?saved=1"} {
		body := send(t, app, "GET", "/plan"+q, testToken, "", nil).Body.String()
		if strings.Contains(body, "Saved.") || strings.Contains(body, `id="plan-result"`) {
			t.Errorf("/plan%s prints a result line", q)
		}
	}

	// A refused save: the same place, the mapped sentence, the choice kept.
	finish := cpByOrd(t, app, 5)
	if err := app.store.SetArrived(finish.ID, parseUTCMust(t, "2030-05-03T22:00:00Z"), nil); err != nil {
		t.Fatal(err)
	}
	rec = browserPost(t, app, "/driver/plan", url.Values{"opt-finish": {"stopover"}})
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/plan?") || !strings.HasSuffix(loc, "#plan-result") {
		t.Fatalf("a refused save: status %d, Location %q", rec.Code, loc)
	}
	refused := send(t, app, "GET", strings.TrimSuffix(loc, "#plan-result"), testToken, "", nil).Body.String()
	want := `<p class="error plan-msg" id="plan-result" role="alert">Not saved: ` +
		template.HTMLEscapeString(planRefusals[planErrConflict]) + `.</p>`
	if !strings.Contains(refused, want) {
		t.Errorf("the page does not say why the save was refused; want %q", want)
	}
	if strings.Contains(refused, "Saved.") {
		t.Error("a refused save says Saved.")
	}
	if !strings.Contains(refused, `name="opt-finish" value="stopover" checked`) {
		t.Error("the refused choice is not still selected on the page")
	}
	if sel := savedSelection(t, app); sel.Choices["finish"] != "direct" {
		t.Errorf("a refused save was stored: %+v", sel)
	}
	// curl is not given a code: it gets the reason, in full.
	if w := postForm(t, app, "/driver/plan", url.Values{"opt-finish": {"stopover"}}); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), "checkpoint 5 is arrived") {
		t.Errorf("curl's refusal: status %d: %s", w.Code, w.Body.String())
	}
}
