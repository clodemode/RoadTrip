package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The checklist fixture is synthetic: an invented checklist about borrowing a
// canoe. Every `private` string in it starts with privateMark, and every
// private note these tests write starts with privateNoteMark, so "no private
// text on this surface" is one substring check with a positive control.
const (
	privateMark     = "PRIVATE-FIXTURE"
	privateNoteMark = "PRIVATE-NOTE-FIXTURE"
	// fixtureVIN has the shape of a vehicle identification number and is not
	// one: 17 letters and digits, no I, O or Q.
	fixtureVIN = "ZZ9FAKE00TEST1234"
)

func fixtureChecklist(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "checklist-fixture.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if n := bytes.Count(raw, []byte(privateMark)); n < 4 {
		t.Fatalf("the fixture holds %d private strings; the private-text tests would pass on nothing", n)
	}
	return raw
}

func pushChecklist(t *testing.T, app *App, body []byte, query string) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, app, "POST", "/driver/checklist"+query, testToken, "application/json", body)
}

// checklistApp is an app showing the fixture trip, with the fixture checklist
// pushed over HTTP the way the trip-keeper pushes one.
func checklistApp(t *testing.T) *App {
	t.Helper()
	app := activeTripApp(t)
	if w := pushChecklist(t, app, fixtureChecklist(t), ""); w.Code != http.StatusOK {
		t.Fatalf("push checklist: status %d: %s", w.Code, w.Body.String())
	}
	return app
}

// mark posts to the check-off route with the token, as curl does.
func mark(t *testing.T, app *App, kind, id string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return postForm(t, app, "/driver/checklist/"+kind+"/"+id, form)
}

func markOK(t *testing.T, app *App, kind, id string, form url.Values) {
	t.Helper()
	if w := mark(t, app, kind, id, form); w.Code != http.StatusOK {
		t.Fatalf("mark %s %s: status %d: %s", kind, id, w.Code, w.Body.String())
	}
}

// census reads the whole checklist with its private text, straight from the
// store.
func census(t *testing.T, app *App, slug string) *Checklist {
	t.Helper()
	cl, err := app.store.Checklist(slug, true)
	if err != nil {
		t.Fatalf("checklist: %v", err)
	}
	if cl == nil {
		t.Fatalf("ride %s has no checklist", slug)
	}
	return cl
}

func item(t *testing.T, app *App, kind, id string) ChecklistItem {
	t.Helper()
	cl := census(t, app, fixtureSlug)
	for _, st := range cl.Stages {
		for _, it := range st.Steps {
			if kind == clStep && it.ID == id {
				return it
			}
		}
	}
	for _, g := range cl.Groups {
		for _, it := range g.Questions {
			if kind == clQuestion && it.ID == id {
				return it
			}
		}
	}
	t.Fatalf("no %s %s in the checklist", kind, id)
	return ChecklistItem{}
}

func checklistFiles(t *testing.T, app *App) []string {
	t.Helper()
	var out []string
	for _, f := range tripFiles(t, app) {
		if strings.Contains(filepath.Base(f), "checklist") {
			out = append(out, f)
		}
	}
	return out
}

// captureLog collects everything the app logs for the rest of the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

// --- A. the document ------------------------------------------------------------------

func TestChecklistPushRequiresACredential(t *testing.T) {
	app := activeTripApp(t)
	for name, token := range map[string]string{"no credential": "", "wrong token": "ffffffffffffffffffffffff"} {
		w := send(t, app, "POST", "/driver/checklist", token, "application/json", fixtureChecklist(t))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, w.Code)
		}
	}
	if files := checklistFiles(t, app); len(files) != 0 {
		t.Errorf("a refused push wrote %v", files)
	}
	if cl, _ := app.store.Checklist(fixtureSlug, true); cl != nil {
		t.Error("a refused push created a checklist")
	}
}

// The whole path: pushed with the token, on the volume byte for byte beside
// the trip file, in the database at once.
func TestChecklistPushStoresAndApplies(t *testing.T) {
	app := activeTripApp(t)
	body := fixtureChecklist(t)
	w := pushChecklist(t, app, body, "")
	if w.Code != http.StatusOK {
		t.Fatalf("push: status %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		Ride      string          `json:"ride"`
		Checklist ChecklistCounts `json:"checklist"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("response: %v", err)
	}
	want := ChecklistCounts{Created: true, Stages: 2, Steps: 5, Questions: 3, Added: 8}
	if res.Ride != fixtureSlug || res.Checklist != want {
		t.Errorf("response %+v; want ride %s and %+v", res, fixtureSlug, want)
	}
	path := checklistPath(app.dataDir, fixtureSlug)
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the checklist file is not on the volume: %v", err)
	}
	if !bytes.Equal(onDisk, body) {
		t.Error("the file on the volume is not the document that was pushed")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("checklist file mode %v, want 0600", fi.Mode().Perm())
	}
	if files := checklistFiles(t, app); len(files) != 1 {
		t.Errorf("the trips dir holds %v; want the one checklist file and no leftover", files)
	}
	cl := census(t, app, fixtureSlug)
	if len(cl.Stages) != 2 || cl.Summary.Total != 5 || cl.Summary.Questions != 3 || len(cl.Sources) != 2 {
		t.Errorf("the stored checklist: %d stages, %+v, %d sources", len(cl.Stages), cl.Summary, len(cl.Sources))
	}
	// A boot pass over the volume finds it and changes nothing.
	if loaded, failed := loadVolumeChecklists(app.store, app.dataDir); loaded != 1 || failed != 0 {
		t.Fatalf("boot pass: %d loaded, %d failed", loaded, failed)
	}
	if again := census(t, app, fixtureSlug); !reflect.DeepEqual(again, cl) {
		t.Error("a boot pass changed the checklist")
	}
	// The trip loader does not mistake the checklist for a trip.
	if loaded, failed := loadVolumeTrips(app.store, app.dataDir); loaded != 1 || failed != 0 {
		t.Errorf("trip boot pass with a checklist beside the trip: %d loaded, %d failed", loaded, failed)
	}
}

// A document is parsed completely before a byte of it is written.
func TestChecklistPushMalformedWritesNothing(t *testing.T) {
	app := activeTripApp(t)
	good := fixtureChecklist(t)
	swap := func(old, new string) []byte { return bytes.Replace(good, []byte(old), []byte(new), 1) }
	bad := map[string][]byte{
		"not json":                    []byte(`{"title": "x", `),
		"a misspelt key":              swap(`"detail": "Cash, in an envelope."`, `"detial": "Cash, in an envelope."`),
		"no title":                    swap(`"title": "Borrowing a canoe for a weekend"`, `"title": " "`),
		"a stage with no zone":        swap(`"tz": "America/Chicago",`, ``),
		"a zone that is not IANA":     swap(`"America/Denver"`, `"Mountain Time"`),
		"a step id used twice":        swap(`"id": "sign-out"`, `"id": "call-ahead"`),
		"an id that is not an id":     swap(`"id": "roof-straps"`, `"id": "Roof Straps"`),
		"a source that is not a link": swap(`"https://example.com/straps"`, `"javascript:alert(1)"`),
		"a step with no title":        swap(`"title": "Sign the book"`, `"title": ""`),
		"something after the end":     append(append([]byte{}, good...), []byte(`{"title": "second"}`)...),
		"nothing to do":               []byte(`{"title": "Empty", "stages": [], "questions": []}`),
		"a ride that is a path":       swap(`"synthetic": true,`, `"synthetic": true, "ride": "../escape",`),
	}
	for name, body := range bad {
		if bytes.Equal(body, good) {
			t.Fatalf("%s: the mutation did not change the fixture", name)
		}
		if w := pushChecklist(t, app, body, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
	if files := checklistFiles(t, app); len(files) != 0 {
		t.Errorf("a malformed push wrote %v", files)
	}
	if cl, _ := app.store.Checklist(fixtureSlug, true); cl != nil {
		t.Error("a malformed push created a checklist")
	}
	// And over a checklist that is already there: nothing moves.
	if w := pushChecklist(t, app, good, ""); w.Code != http.StatusOK {
		t.Fatalf("push: %d %s", w.Code, w.Body.String())
	}
	before := census(t, app, fixtureSlug)
	for name, body := range bad {
		if w := pushChecklist(t, app, body, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s over an existing checklist: status %d", name, w.Code)
		}
	}
	if onDisk, _ := os.ReadFile(checklistPath(app.dataDir, fixtureSlug)); !bytes.Equal(onDisk, good) {
		t.Error("a malformed push replaced the file on the volume")
	}
	if after := census(t, app, fixtureSlug); !reflect.DeepEqual(after, before) {
		t.Error("a malformed push changed the stored checklist")
	}
	if files := checklistFiles(t, app); len(files) != 1 {
		t.Errorf("a malformed push left %v", files)
	}
}

// The ride a checklist lands on: ?ride=, else the document's, else the ride
// the site shows — and it has to exist.
func TestChecklistPushNamesItsRide(t *testing.T) {
	app := activeTripApp(t)
	good := fixtureChecklist(t)
	named := bytes.Replace(good, []byte(`"synthetic": true,`), []byte(`"synthetic": true, "ride": "fixture-trip",`), 1)
	if w := pushChecklist(t, app, named, "?ride="+app.rideSlug); w.Code != http.StatusBadRequest {
		t.Errorf("?ride= against the document's ride: status %d, want 400", w.Code)
	}
	if w := pushChecklist(t, app, good, "?ride=no-such-ride"); w.Code != http.StatusNotFound {
		t.Errorf("a ride that is not in the database: status %d, want 404", w.Code)
	}
	if w := pushChecklist(t, app, good, "?ride=../escape"); w.Code != http.StatusBadRequest {
		t.Errorf("a ride that is a path: status %d, want 400", w.Code)
	}
	if files := checklistFiles(t, app); len(files) != 0 {
		t.Fatalf("a refused push wrote %v", files)
	}
	// Named for the earlier ride while the fixture trip is showing: it lands on
	// the earlier ride and the page being shown does not get it.
	if w := pushChecklist(t, app, good, "?ride="+app.rideSlug); w.Code != http.StatusOK {
		t.Fatalf("push for the earlier ride: %d %s", w.Code, w.Body.String())
	}
	if cl, _ := app.store.Checklist(fixtureSlug, true); cl != nil {
		t.Error("the active ride got a checklist that was pushed for another ride")
	}
	if census(t, app, app.rideSlug).Summary.Total != 5 {
		t.Error("the named ride did not get its checklist")
	}
	if body := send(t, app, "GET", "/checklist", "", "", nil).Body.String(); strings.Contains(body, "Call the boathouse") {
		t.Error("/checklist shows another ride's checklist")
	}
	if w := pushChecklist(t, app, named, ""); w.Code != http.StatusOK {
		t.Fatalf("push by the document's own ride: %d %s", w.Code, w.Body.String())
	}
	if census(t, app, fixtureSlug).Summary.Total != 5 {
		t.Error("the document's own ride did not get its checklist")
	}
}

// --- B. public and private ---------------------------------------------------------------

func TestHasVINShape(t *testing.T) {
	yes := []string{fixtureVIN, strings.ToLower(fixtureVIN), "the number is " + fixtureVIN + ".",
		"(" + fixtureVIN + ")", "VIN:" + fixtureVIN, fixtureVIN + "\nnext line", "12345678901234567"}
	no := []string{"", "an ordinary sentence about a canoe", fixtureVIN[:16], fixtureVIN + "5",
		"ZZ9FAKEO0TEST1234", // an O: not a VIN character
		"ZZ9FAKEI0TEST1234", "ZZ9FAKEQ0TEST1234",
		"ABCDEFGHJKLMNPRST",                      // 17 letters and no digit: a word, not an identifier
		"https://example.org/boathouse/handbook", // a web address
		"2030-05-01T14:30:00-06:00",
	}
	for _, s := range yes {
		if !hasVINShape(s) {
			t.Errorf("not recognised as VIN-shaped: %q", s)
		}
	}
	for _, s := range no {
		if hasVINShape(s) {
			t.Errorf("wrongly recognised as VIN-shaped: %q", s)
		}
	}
}

// A VIN-shaped token in any public field refuses the push, and the refusal
// names the field: the value appears in no response and no log line.
func TestChecklistPushRefusesAVINShapeWithoutEchoingIt(t *testing.T) {
	app := activeTripApp(t)
	logged := captureLog(t)
	good := fixtureChecklist(t)
	swap := func(old, new string) []byte { return bytes.Replace(good, []byte(old), []byte(new), 1) }
	cases := []struct {
		name, field string
		body        []byte
	}{
		{"the title", "title", swap(`"title": "Borrowing a canoe for a weekend"`, `"title": "Canoe `+fixtureVIN+`"`)},
		{"the intro", "intro", swap(`"intro": "An invented`, `"intro": "`+fixtureVIN+` An invented`)},
		{"a stage's when", "stages[1].when", swap(`"when": "Day 2, mid-morning"`, `"when": "`+fixtureVIN+`"`)},
		{"a step title", "stages[0].steps[1].title", swap(`"title": "Pack two roof straps"`, `"title": "Pack `+fixtureVIN+`"`)},
		{"a step detail", "stages[0].steps[0].detail", swap(`"detail": "Confirm the canoe is free`, `"detail": "(`+fixtureVIN+`) Confirm the canoe is free`)},
		{"a step detail, lower case", "stages[0].steps[2].detail", swap(`"detail": "Cash, in an envelope."`, `"detail": "`+strings.ToLower(fixtureVIN)+`"`)},
		{"a step id", "stages[1].steps[1].id", swap(`"id": "sign-out"`, `"id": "`+strings.ToLower(fixtureVIN)+`"`)},
		{"a question", "questions[0].ask", swap(`"ask": "Do the paddles come with it?"`, `"ask": "Is it `+fixtureVIN+`?"`)},
		{"a question's why", "questions[2].why", swap(`"why": "The bars are close together."`, `"why": "`+fixtureVIN+`"`)},
		{"a question's group", "questions[2].group", swap(`"group": "The car"`, `"group": "`+fixtureVIN+`"`)},
		{"a source label", "sources[0].label", swap(`"label": "An invented boathouse handbook"`, `"label": "`+fixtureVIN+`"`)},
		{"a source address", "sources[1].url", swap(`"https://example.com/straps"`, `"https://example.com/`+fixtureVIN+`"`)},
		{"a key", "", swap(`"synthetic": true,`, `"synthetic": true, "`+fixtureVIN+`": 1,`)},
	}
	for _, c := range cases {
		if bytes.Equal(c.body, good) {
			t.Fatalf("%s: the mutation did not change the fixture", c.name)
		}
		w := pushChecklist(t, app, c.body, "")
		body := w.Body.String()
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", c.name, w.Code, body)
		}
		if c.field != "" && !strings.Contains(body, c.field+" holds") {
			t.Errorf("%s: the refusal does not name the field %s: %s", c.name, c.field, body)
		}
		if strings.Contains(strings.ToLower(body), strings.ToLower(fixtureVIN)) {
			t.Errorf("%s: the refusal echoes the value", c.name)
		}
	}
	if files := checklistFiles(t, app); len(files) != 0 {
		t.Errorf("a refused push wrote %v", files)
	}
	if cl, _ := app.store.Checklist(fixtureSlug, true); cl != nil {
		t.Error("a refused push created a checklist")
	}
	// Positive control: the same token in a PRIVATE field is where it belongs.
	private := swap(`"private": "PRIVATE-FIXTURE hull tag CANOE-TAG-0042"`, `"private": "PRIVATE-FIXTURE `+fixtureVIN+`"`)
	if w := pushChecklist(t, app, private, ""); w.Code != http.StatusOK {
		t.Fatalf("a VIN shape in a private field was refused: %d %s", w.Code, w.Body.String())
	}
	if got := item(t, app, clStep, "inspect").Private; !strings.Contains(got, fixtureVIN) {
		t.Fatalf("the private field did not keep its text: the checks below would pass on nothing")
	}
	// A public note at check-off is held to the same rule.
	w := mark(t, app, clStep, "inspect", url.Values{"outcome": {"hull " + fixtureVIN + " looked fine"}})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "What happened holds") {
		t.Errorf("a VIN shape in a public note: status %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), fixtureVIN) {
		t.Error("the check-off refusal echoes the value")
	}
	if it := item(t, app, clStep, "inspect"); it.Done || it.Outcome != "" {
		t.Errorf("a refused check-off changed the step: %+v", it)
	}
	// The browser path is a redirect, and it carries the field and not the value.
	req := httptest.NewRequest("POST", "/driver/checklist/question/q-roof",
		strings.NewReader(url.Values{"outcome": {fixtureVIN}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	app.routes().ServeHTTP(rec, req)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.Contains(loc, "error=") || !strings.HasSuffix(loc, "#question-q-roof") {
		t.Errorf("the browser refusal: status %d, Location %q", rec.Code, loc)
	}
	if strings.Contains(loc, fixtureVIN) {
		t.Error("the browser refusal echoes the value in its redirect")
	}
	// The private note may hold it.
	markOK(t, app, clStep, "inspect", url.Values{"private_note": {privateNoteMark + " " + fixtureVIN}})
	// Nothing logged it, at any point above — and the log did see the refusals.
	if !strings.Contains(logged.String(), "checklist push: refused") {
		t.Fatal("the log shows no refused push; the log check below would pass on nothing")
	}
	if strings.Contains(strings.ToLower(logged.String()), strings.ToLower(fixtureVIN)) {
		t.Error("a log line carries the VIN-shaped value")
	}
	// And the public surfaces do not have it, though the store does.
	for _, p := range []string{"/checklist", "/checklist.json", "/"} {
		if body := send(t, app, "GET", p, "", "", nil).Body.String(); strings.Contains(body, fixtureVIN) {
			t.Errorf("%s carries the VIN-shaped value from a private field", p)
		}
	}
}

// privateTextApp is a checklist with every kind of private text in it: the
// document's own, and a private note on a done step and on an answered
// question — each beside a public note that must show.
func privateTextApp(t *testing.T) *App {
	t.Helper()
	app := checklistApp(t)
	markOK(t, app, clStep, "call-ahead", url.Values{
		"outcome":      {"They open at seven, not eight."},
		"private_note": {privateNoteMark + " the clerk was Bramble Thistlewood"},
	})
	markOK(t, app, clQuestion, "q-paddles", url.Values{
		"outcome":      {"Two paddles, one spare."},
		"private_note": {privateNoteMark + " paddle locker code 7-7-7"},
	})
	// An open step that carries notes from an undone check-off.
	markOK(t, app, clStep, "deposit", url.Values{
		"outcome":      {"Paid and then refunded."},
		"private_note": {privateNoteMark + " receipt in the glovebox"},
	})
	markOK(t, app, clStep, "deposit", url.Values{"clear": {"1"}})
	return app
}

// THE ANONYMOUS-RENDER GUARD. The page a visitor gets carries the public text
// and none of the private text; the same page, signed in, carries both.
func TestChecklistPageShowsPrivateTextOnlyWhenSignedIn(t *testing.T) {
	app := privateTextApp(t)

	anon := send(t, app, "GET", "/checklist", "", "", nil)
	if anon.Code != http.StatusOK {
		t.Fatalf("anonymous /checklist: status %d", anon.Code)
	}
	page := anon.Body.String()
	// Positive control: this is the checklist page, with its public text on it.
	for _, want := range []string{"Borrowing a canoe for a weekend", "One paddler&#39;s notes, not advice.",
		"Call the boathouse", "Confirm the canoe is free", "They open at seven, not eight.",
		"Do the paddles come with it?", "Two paddles, one spare.", "An invented boathouse handbook",
		"1 of 5 done", "1 of 3 questions answered"} {
		if !strings.Contains(page, want) {
			t.Errorf("the anonymous page lacks its public text %q", want)
		}
	}
	for _, mark := range []string{privateMark, privateNoteMark, "Private note"} {
		if strings.Contains(page, mark) {
			t.Errorf("the anonymous page carries private text (%s)", mark)
		}
	}
	// No way to change anything either, and an undone step's note is not shown.
	for _, control := range []string{"<form method=\"post\" action=\"/driver/checklist", "<details", "Paid and then refunded."} {
		if strings.Contains(page, control) {
			t.Errorf("the anonymous page carries %q", control)
		}
	}

	signedIn := send(t, app, "GET", "/checklist", testToken, "", nil).Body.String()
	if n := strings.Count(signedIn, privateMark); n != 4 {
		t.Errorf("the signed-in page shows %d of the document's 4 private strings", n)
	}
	if n := strings.Count(signedIn, privateNoteMark); n < 3 {
		t.Errorf("the signed-in page shows %d private notes; want the three that were written", n)
	}
	for _, want := range []string{`action="/driver/checklist/step/call-ahead"`, `action="/driver/checklist/question/q-late"`,
		"Mark done", "What happened", "Private note", ">Undo<"} {
		if !strings.Contains(signedIn, want) {
			t.Errorf("the signed-in page lacks %q", want)
		}
	}
}

// Every open endpoint, with and without a credential, and the census that is
// allowed to carry it.
func TestChecklistOpenEndpointsCarryNoPrivateText(t *testing.T) {
	app := privateTextApp(t)
	open := []string{"/", "/checklist.json", "/plan", "/plan.json", "/route.geojson", "/photos",
		"/legs/day-1-ridge", "/healthz", "/robots.txt", "/driver/login"}
	for _, token := range []string{"", testToken} {
		for _, p := range open {
			w := send(t, app, "GET", p, token, "", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("%s: status %d", p, w.Code)
			}
			for _, mark := range []string{privateMark, privateNoteMark} {
				if strings.Contains(w.Body.String(), mark) {
					t.Errorf("%s (credential: %v) carries private text (%s)", p, token != "", mark)
				}
			}
		}
	}
	// /checklist.json is the public page as data: it has the public notes.
	data := send(t, app, "GET", "/checklist.json", "", "", nil).Body.String()
	for _, want := range []string{"They open at seven, not eight.", "Two paddles, one spare.", `"steps_done": 1`} {
		if !strings.Contains(data, want) {
			t.Errorf("/checklist.json lacks %q", want)
		}
	}
	if strings.Contains(data, "Paid and then refunded.") {
		t.Error("/checklist.json carries the note of a step that was reopened")
	}
	// The census is signed-in only, and it is where the private text is.
	if w := send(t, app, "GET", "/driver/checklist.json", "", "", nil); w.Code != http.StatusFound ||
		strings.Contains(w.Body.String(), privateMark) {
		t.Errorf("the census without a credential: status %d", w.Code)
	}
	full := send(t, app, "GET", "/driver/checklist.json", testToken, "", nil)
	if full.Code != http.StatusOK || strings.Count(full.Body.String(), privateMark) != 4 ||
		strings.Count(full.Body.String(), privateNoteMark) != 3 {
		t.Errorf("the census with the token: status %d, %d private strings, %d private notes", full.Code,
			strings.Count(full.Body.String(), privateMark), strings.Count(full.Body.String(), privateNoteMark))
	}
	if cc := full.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("the census is cacheable: %q", cc)
	}
	// While the whole site is private the page is as closed as every other.
	app.sitePrivate = true
	for _, p := range []string{"/checklist", "/checklist.json"} {
		w := send(t, app, "GET", p, "", "", nil)
		if w.Code != http.StatusFound || strings.Contains(w.Body.String(), "canoe") {
			t.Errorf("%s while the site is private: status %d", p, w.Code)
		}
	}
}

// The store seam itself: a read without private does not hold private text
// anywhere in the value it returns.
func TestChecklistPublicReadHoldsNoPrivateText(t *testing.T) {
	app := privateTextApp(t)
	pub, err := app.store.Checklist(fixtureSlug, false)
	if err != nil || pub == nil {
		t.Fatalf("public read: %v", err)
	}
	raw, _ := json.Marshal(pub)
	dump := string(raw) + reflectStrings(reflect.ValueOf(*pub))
	if strings.Contains(dump, privateMark) || strings.Contains(dump, privateNoteMark) {
		t.Error("a public read of the checklist holds private text")
	}
	if !strings.Contains(dump, "They open at seven, not eight.") {
		t.Fatal("the public read lacks its public notes; the check above would pass on nothing")
	}
	full, _ := json.Marshal(census(t, app, fixtureSlug))
	if strings.Count(string(full), privateMark) != 4 {
		t.Error("the private read does not hold the document's private text")
	}
}

// reflectStrings collects every string reachable in v, exported or not, so a
// field with no JSON name cannot hide text from the check above.
func reflectStrings(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return v.String() + "\n"
	case reflect.Struct:
		var b strings.Builder
		for i := 0; i < v.NumField(); i++ {
			b.WriteString(reflectStrings(v.Field(i)))
		}
		return b.String()
	case reflect.Slice, reflect.Array:
		var b strings.Builder
		for i := 0; i < v.Len(); i++ {
			b.WriteString(reflectStrings(v.Index(i)))
		}
		return b.String()
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return ""
		}
		return reflectStrings(v.Elem())
	}
	return ""
}

// --- C. checking off ---------------------------------------------------------------------

func TestChecklistMarkRequiresACredential(t *testing.T) {
	app := checklistApp(t)
	for _, token := range []string{"", "ffffffffffffffffffffffff"} {
		w := send(t, app, "POST", "/driver/checklist/step/call-ahead", token, "application/x-www-form-urlencoded", nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("token %q: status %d, want 401", token, w.Code)
		}
	}
	if it := item(t, app, clStep, "call-ahead"); it.Done {
		t.Error("a refused check-off marked the step done")
	}
	for _, target := range []string{"/driver/checklist/stage/pickup", "/driver/checklist/step/no-such-step",
		"/driver/checklist/step/Not%20An%20Id"} {
		if w := postForm(t, app, target, url.Values{}); w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, w.Code)
		}
	}
}

// A tap is "now", in the stage's zone.
func TestChecklistCheckOffNow(t *testing.T) {
	app := checklistApp(t)
	before := time.Now().Add(-2 * time.Second)
	w := mark(t, app, clStep, "call-ahead", url.Values{})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	it := item(t, app, clStep, "call-ahead")
	at, ok := parseUTC(it.DoneAt)
	if !it.Done || !ok || at.Before(before) || at.After(time.Now().Add(2*time.Second)) {
		t.Errorf("the stamp is not now: %+v", it)
	}
	if !strings.HasSuffix(it.DoneAt, "Z") {
		t.Errorf("the stamp is not stored as UTC: %q", it.DoneAt)
	}
	if it.DoneTZ != "America/Chicago" {
		t.Errorf("the stamp's zone is %q, want the stage's, America/Chicago", it.DoneTZ)
	}
	if !strings.Contains(w.Body.String(), `"steps_done": 1`) {
		t.Errorf("the response does not carry the new count: %s", w.Body.String())
	}
	// A browser gets sent back to the step it tapped.
	req := httptest.NewRequest("POST", "/driver/checklist/step/roof-straps", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	app.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/checklist#step-roof-straps" {
		t.Errorf("the browser check-off: status %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
	if !item(t, app, clStep, "roof-straps").Done {
		t.Error("the browser check-off did not land")
	}
}

// After the fact is the normal case: the trip-keeper types the wall clock,
// and it is read in the STAGE's zone whatever the season.
func TestChecklistCheckOffAfterTheFactInTheStagesZone(t *testing.T) {
	app := checklistApp(t)
	// "inspect" is in the dock stage: America/Denver.
	cases := []struct{ at, wantUTC, wantShown string }{
		{"2026-07-01T09:30", "2026-07-01T15:30:00Z", "Wed Jul 1 · 9:30 AM MDT"},          // daylight time, UTC-6
		{"2026-01-15T09:30", "2026-01-15T16:30:00Z", "Thu Jan 15 · 9:30 AM MST"},         // standard time, UTC-7
		{"2026-07-01T11:30:00-04:00", "2026-07-01T15:30:00Z", "Wed Jul 1 · 9:30 AM MDT"}, // an offset of its own
		{"2026-07-01T15:30:00Z", "2026-07-01T15:30:00Z", "Wed Jul 1 · 9:30 AM MDT"},      // UTC
		{"2026-07-01T09:30:45", "2026-07-01T15:30:45Z", "Wed Jul 1 · 9:30 AM MDT"},       // with seconds
		{"2026-02-28T23:59", "2026-03-01T06:59:00Z", "Sat Feb 28 · 11:59 PM MST"},        // crosses the UTC day
		{"2026-01-15T09:30:00-07:00", "2026-01-15T16:30:00Z", "Thu Jan 15 · 9:30 AM MST"},
	}
	for _, c := range cases {
		markOK(t, app, clStep, "inspect", url.Values{"clear": {"1"}})
		markOK(t, app, clStep, "inspect", url.Values{"at": {c.at}})
		it := item(t, app, clStep, "inspect")
		if it.DoneAt != c.wantUTC || it.DoneTZ != "America/Denver" {
			t.Errorf("at=%s: stored %s in %s; want %s in America/Denver", c.at, it.DoneAt, it.DoneTZ, c.wantUTC)
		}
		if page := send(t, app, "GET", "/checklist", "", "", nil).Body.String(); !strings.Contains(page, c.wantShown) {
			t.Errorf("at=%s: the page does not show %q", c.at, c.wantShown)
		}
	}
	// A step in the other stage reads the same wall clock in ITS zone.
	markOK(t, app, clStep, "call-ahead", url.Values{"at": {"2026-07-01T09:30"}})
	if it := item(t, app, clStep, "call-ahead"); it.DoneAt != "2026-07-01T14:30:00Z" || it.DoneTZ != "America/Chicago" {
		t.Errorf("the Chicago stage: stored %s in %s", it.DoneAt, it.DoneTZ)
	}
	// What is refused, and that a refusal changes nothing.
	want := item(t, app, clStep, "inspect")
	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	for name, form := range map[string]url.Values{
		"the future":       {"at": {future}},
		"not a time":       {"at": {"last Tuesday"}},
		"the zero time":    {"at": {"0001-01-01T00:00:00Z"}},
		"a time and clear": {"at": {"2026-07-01T09:30"}, "clear": {"1"}},
	} {
		if w := mark(t, app, clStep, "inspect", form); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
	if got := item(t, app, clStep, "inspect"); !reflect.DeepEqual(got, want) {
		t.Errorf("a refused time changed the step: %+v -> %+v", want, got)
	}
}

// Correcting a note does not move the time: posting again keeps the stamp
// unless a new time is given, and the page's own form posting the old time
// back to the minute is not a new time.
func TestChecklistEditingADoneStepKeepsItsStamp(t *testing.T) {
	app := checklistApp(t)
	markOK(t, app, clStep, "inspect", url.Values{"at": {"2026-07-01T09:30:45"}, "outcome": {"One scratch, photographed."}})
	stamp := item(t, app, clStep, "inspect").DoneAt
	markOK(t, app, clStep, "inspect", url.Values{"outcome": {"Two scratches, photographed."}})
	markOK(t, app, clStep, "inspect", url.Values{"at": {"2026-07-01T09:30"}, "private_note": {privateNoteMark + " x"}})
	it := item(t, app, clStep, "inspect")
	if it.DoneAt != stamp || it.Outcome != "Two scratches, photographed." || it.PrivateNote != privateNoteMark+" x" {
		t.Errorf("after two edits: %+v; want the stamp %s kept and both notes saved", it, stamp)
	}
	// A bare "done" from curl does not wipe the notes.
	markOK(t, app, clStep, "inspect", url.Values{})
	if got := item(t, app, clStep, "inspect"); !reflect.DeepEqual(got, it) {
		t.Errorf("a bare re-post changed the step: %+v -> %+v", it, got)
	}
	// A new time does move it.
	markOK(t, app, clStep, "inspect", url.Values{"at": {"2026-07-02T10:00"}})
	if got := item(t, app, clStep, "inspect").DoneAt; got != "2026-07-02T16:00:00Z" {
		t.Errorf("a new time was not taken: %s", got)
	}
}

// Undo clears the stamp and nothing else.
func TestChecklistUndo(t *testing.T) {
	app := checklistApp(t)
	markOK(t, app, clStep, "call-ahead", url.Values{"outcome": {"They open at seven."}, "private_note": {privateNoteMark + " kept"}})
	if sum, _ := app.store.ChecklistSummary(fixtureSlug); sum.Done != 1 {
		t.Fatalf("the step was not done: %+v", sum)
	}
	w := mark(t, app, clStep, "call-ahead", url.Values{"clear": {"1"}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"done": false`) {
		t.Fatalf("undo: status %d: %s", w.Code, w.Body.String())
	}
	it := item(t, app, clStep, "call-ahead")
	if it.Done || it.DoneAt != "" || it.DoneTZ != "" {
		t.Errorf("undo left a stamp: %+v", it)
	}
	if it.Outcome != "They open at seven." || it.PrivateNote != privateNoteMark+" kept" {
		t.Errorf("undo dropped the notes: %+v", it)
	}
	if sum, _ := app.store.ChecklistSummary(fixtureSlug); sum.Done != 0 {
		t.Errorf("the count after undo: %+v", sum)
	}
	page := send(t, app, "GET", "/checklist", "", "", nil).Body.String()
	if strings.Contains(page, "They open at seven.") || !strings.Contains(page, "0 of 5 done") {
		t.Error("the public page still shows the undone step as done, or its note")
	}
	// Undoing an open step is a no-op, not an error.
	if w := mark(t, app, clStep, "call-ahead", url.Values{"clear": {"1"}}); w.Code != http.StatusOK {
		t.Errorf("undo twice: status %d", w.Code)
	}
}

// A question is answered the same way: a public answer, a private note, a
// time in the zone the ride is in.
func TestChecklistAnswerAQuestion(t *testing.T) {
	app := checklistApp(t)
	zone := app.rideZone(fixtureSlug)
	if _, err := time.LoadLocation(zone); err != nil || zone == "UTC" {
		t.Fatalf("the fixture ride's zone is %q; this test needs a real one", zone)
	}
	markOK(t, app, clQuestion, "q-paddles", url.Values{
		"outcome": {"Two paddles, one spare."}, "private_note": {privateNoteMark + " locker code"},
		"at": {"2026-07-01T09:30"},
	})
	it := item(t, app, clQuestion, "q-paddles")
	loc, _ := time.LoadLocation(zone)
	want := utcString(time.Date(2026, 7, 1, 9, 30, 0, 0, loc))
	if !it.Done || it.DoneAt != want || it.DoneTZ != zone || it.Outcome != "Two paddles, one spare." {
		t.Errorf("the answer: %+v; want %s in %s", it, want, zone)
	}
	sum, _ := app.store.ChecklistSummary(fixtureSlug)
	if sum.Answered != 1 || sum.Questions != 3 || sum.Done != 0 {
		t.Errorf("the count: %+v", sum)
	}
	page := send(t, app, "GET", "/checklist", "", "", nil).Body.String()
	for _, want := range []string{"Two paddles, one spare.", "1 of 3 answered", "Answered <time"} {
		if !strings.Contains(page, want) {
			t.Errorf("the public page lacks %q", want)
		}
	}
	if strings.Contains(page, privateNoteMark) {
		t.Error("the public page carries the question's private note")
	}
	// tz= names another zone for a question; a zone that is not one is refused.
	markOK(t, app, clQuestion, "q-roof", url.Values{"at": {"2026-07-01T09:30"}, "tz": {"America/Denver"}})
	if it := item(t, app, clQuestion, "q-roof"); it.DoneAt != "2026-07-01T15:30:00Z" || it.DoneTZ != "America/Denver" {
		t.Errorf("a question answered in another zone: %+v", it)
	}
	if w := mark(t, app, clQuestion, "q-late", url.Values{"tz": {"Mountain Time"}}); w.Code != http.StatusBadRequest {
		t.Errorf("a zone that is not IANA: status %d", w.Code)
	}
	// Undo works on a question too.
	markOK(t, app, clQuestion, "q-paddles", url.Values{"clear": {"1"}})
	if item(t, app, clQuestion, "q-paddles").Done {
		t.Error("the question is still answered after undo")
	}
}

// THE RE-PUSH RULE. A later document refreshes what is open and never writes
// what is done: not its text, not its private text, not its stamp or notes.
// What it no longer has is dropped if open and kept if done.
func TestChecklistRePushRefreshesOpenAndNeverTouchesDone(t *testing.T) {
	app := checklistApp(t)
	markOK(t, app, clStep, "call-ahead", url.Values{"at": {"2026-07-01T09:30"},
		"outcome": {"They open at seven."}, "private_note": {privateNoteMark + " clerk"}})
	markOK(t, app, clStep, "inspect", url.Values{"at": {"2026-07-02T10:00"}})
	markOK(t, app, clQuestion, "q-paddles", url.Values{"outcome": {"Two paddles."}})
	doneBefore := map[string]ChecklistItem{
		"call-ahead": item(t, app, clStep, "call-ahead"),
		"inspect":    item(t, app, clStep, "inspect"),
		"q-paddles":  item(t, app, clQuestion, "q-paddles"),
	}

	// The later document: every done item reworded (and one re-zoned by
	// dropping its whole stage), every open item reworded or removed, one added.
	later := []byte(`{
	  "synthetic": true,
	  "title": "Borrowing a canoe, second draft",
	  "intro": "Reworded.",
	  "disclaimer": "",
	  "stages": [
	    {"id": "pickup", "title": "Pickup, renamed", "tz": "America/Denver", "when": "Sooner",
	     "steps": [
	       {"id": "call-ahead", "title": "REWORDED DONE STEP", "detail": "REWORDED DONE DETAIL", "private": "PRIVATE-FIXTURE reworded done private"},
	       {"id": "roof-straps", "title": "Pack three roof straps", "detail": "One spare.", "private": "PRIVATE-FIXTURE new private on an open step"},
	       {"id": "life-jackets", "title": "Borrow two life jackets", "detail": "", "private": ""}
	     ]}
	  ],
	  "questions": [
	    {"id": "q-paddles", "group": "Renamed group", "ask": "REWORDED ANSWERED QUESTION", "why": "", "private": ""},
	    {"id": "q-late", "group": "Gear", "ask": "Is there a fee for a late return, and how much?", "why": "Reworded.", "private": ""}
	  ],
	  "sources": [{"label": "One source now", "url": "https://example.org/one"}]
	}`)
	w := pushChecklist(t, app, later, "")
	if w.Code != http.StatusOK {
		t.Fatalf("re-push: status %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		Checklist ChecklistCounts `json:"checklist"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	// kept: call-ahead and q-paddles (in the document), inspect (not in it).
	// refreshed: roof-straps, q-late. added: life-jackets. dropped: deposit,
	// sign-out, q-roof.
	want := ChecklistCounts{Stages: 1, Steps: 3, Questions: 2, Added: 1, Refreshed: 2, KeptDone: 3, Dropped: 3}
	if res.Checklist != want {
		t.Errorf("the re-push reports %+v; want %+v", res.Checklist, want)
	}

	// Done items: identical, field for field, private text included.
	for id, before := range doneBefore {
		after := item(t, app, before.Kind, id)
		// FormTZ is the stage's current zone, which the document owns; the
		// stamp's own zone (DoneTZ) is the item's and must not have moved.
		before.FormTZ, after.FormTZ = "", ""
		if !reflect.DeepEqual(after, before) {
			t.Errorf("the re-push touched done item %s:\n before %+v\n after  %+v", id, before, after)
		}
	}
	cl := census(t, app, fixtureSlug)
	all, _ := json.Marshal(cl)
	if strings.Contains(string(all), "REWORDED") || strings.Contains(string(all), "reworded done private") {
		t.Error("a done item took text from the later document")
	}
	// The done step in the dropped stage is still shown, in its stage, with
	// the stamp in the zone it was made in.
	var dock *ChecklistStage
	for i := range cl.Stages {
		if cl.Stages[i].ID == "dock" {
			dock = &cl.Stages[i]
		}
	}
	if dock == nil || len(dock.Steps) != 1 || dock.Steps[0].ID != "inspect" || dock.Steps[0].DoneTZ != "America/Denver" {
		t.Errorf("the dropped stage with a done step in it: %+v", dock)
	}
	if got := item(t, app, clStep, "call-ahead"); got.DoneTZ != "America/Chicago" || got.DoneAt != "2026-07-01T14:30:00Z" {
		t.Errorf("a done step's stamp moved when its stage changed zone: %+v", got)
	}

	// Open items: the later document's text.
	if it := item(t, app, clStep, "roof-straps"); it.Title != "Pack three roof straps" || it.Detail != "One spare." ||
		!strings.Contains(it.Private, "new private on an open step") {
		t.Errorf("an open step was not refreshed: %+v", it)
	}
	if it := item(t, app, clQuestion, "q-late"); !strings.Contains(it.Title, "and how much") {
		t.Errorf("an open question was not refreshed: %+v", it)
	}
	if it := item(t, app, clStep, "life-jackets"); it.Done || it.Title != "Borrow two life jackets" {
		t.Errorf("the new step: %+v", it)
	}
	// Open items the document dropped are gone.
	for _, gone := range []string{`"deposit"`, `"sign-out"`, `"q-roof"`} {
		if strings.Contains(string(all), `"id":`+gone) {
			t.Errorf("an open item the later document dropped is still there: %s", gone)
		}
	}
	// The header is the document's, and an empty disclaimer still shows one.
	if cl.Title != "Borrowing a canoe, second draft" || len(cl.Sources) != 1 || cl.Disclaimer != defaultDisclaimer {
		t.Errorf("the header after the re-push: %q, %d sources, disclaimer %q", cl.Title, len(cl.Sources), cl.Disclaimer)
	}
	if cl.Summary != (ChecklistSummary{Done: 2, Total: 4, Answered: 1, Questions: 2}) {
		t.Errorf("the count after the re-push: %+v", cl.Summary)
	}
	page := send(t, app, "GET", "/checklist", "", "", nil).Body.String()
	if !strings.Contains(page, "One traveller&#39;s notes, not advice.") {
		t.Error("the page shows no disclaimer when the document gives none")
	}

	// Pushing the first document again is held to the same rule, and so is a boot.
	if w := pushChecklist(t, app, fixtureChecklist(t), ""); w.Code != http.StatusOK {
		t.Fatalf("third push: %d", w.Code)
	}
	if loaded, failed := loadVolumeChecklists(app.store, app.dataDir); loaded != 1 || failed != 0 {
		t.Fatalf("boot pass: %d loaded, %d failed", loaded, failed)
	}
	for id, before := range doneBefore {
		after := item(t, app, before.Kind, id)
		before.FormTZ, after.FormTZ = "", ""
		if !reflect.DeepEqual(after, before) {
			t.Errorf("a third push or a boot touched done item %s", id)
		}
	}
	// Reopened, an item is the document's again.
	markOK(t, app, clStep, "call-ahead", url.Values{"clear": {"1"}})
	if w := pushChecklist(t, app, later, ""); w.Code != http.StatusOK {
		t.Fatalf("fourth push: %d", w.Code)
	}
	if it := item(t, app, clStep, "call-ahead"); it.Title != "REWORDED DONE STEP" {
		t.Errorf("a reopened step did not take the document's text: %+v", it)
	}
}

// --- D. the page ---------------------------------------------------------------------------

func TestFrontPageSummarisesTheChecklist(t *testing.T) {
	app := activeTripApp(t)
	// No checklist: no line, and no link in the masthead.
	home := send(t, app, "GET", "/", "", "", nil).Body.String()
	if strings.Contains(home, `href="/checklist"`) {
		t.Error("/ links to a checklist the ride does not have")
	}
	if w := send(t, app, "GET", "/checklist", "", "", nil); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), "This trip has no checklist.") {
		t.Errorf("/checklist for a ride with none: status %d", w.Code)
	}
	if w := send(t, app, "GET", "/checklist.json", "", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("/checklist.json for a ride with none: status %d, want 404", w.Code)
	}

	if w := pushChecklist(t, app, fixtureChecklist(t), ""); w.Code != http.StatusOK {
		t.Fatalf("push: %d", w.Code)
	}
	home = send(t, app, "GET", "/", "", "", nil).Body.String()
	if !strings.Contains(home, `<a href="/checklist">Checklist</a>: 0 of 5 done &middot; 0 of 3 questions answered`) {
		t.Error("/ does not summarise the checklist as 0 of 5 done")
	}
	if strings.Count(home, `href="/checklist"`) != 2 {
		t.Errorf("/ has %d links to the checklist; want the masthead's and the summary's", strings.Count(home, `href="/checklist"`))
	}
	markOK(t, app, clStep, "call-ahead", url.Values{})
	markOK(t, app, clStep, "inspect", url.Values{})
	markOK(t, app, clQuestion, "q-late", url.Values{})
	home = send(t, app, "GET", "/", "", "", nil).Body.String()
	if !strings.Contains(home, `Checklist</a>: 2 of 5 done &middot; 1 of 3 questions answered`) {
		t.Error("/ does not summarise the checklist as 2 of 5 done")
	}
	// Every page's masthead reaches it.
	for _, p := range []string{"/plan", "/photos", "/legs/day-1-ridge"} {
		if body := send(t, app, "GET", p, "", "", nil).Body.String(); !strings.Contains(body, `<a href="/checklist">Checklist</a>`) {
			t.Errorf("%s has no masthead link to the checklist", p)
		}
	}
	// The stage progress on the page itself.
	page := send(t, app, "GET", "/checklist", "", "", nil).Body.String()
	for _, want := range []string{"2 of 5 done", ">1 of 3 done<", ">1 of 2 done<", "The evening before, at Alder"} {
		if !strings.Contains(page, want) {
			t.Errorf("/checklist lacks %q", want)
		}
	}
	// The earlier ride has no checklist: showing it, the line and the link go.
	if w := postForm(t, app, "/driver/ride/active", url.Values{"clear": {"1"}}); w.Code != http.StatusOK {
		t.Fatalf("clear the active ride: %d", w.Code)
	}
	if home := send(t, app, "GET", "/", "", "", nil).Body.String(); strings.Contains(home, `href="/checklist"`) {
		t.Error("the earlier ride's front page links to the other ride's checklist")
	}
}
