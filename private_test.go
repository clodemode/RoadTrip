package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The private overlay is the one piece of volume data with a canonical copy
// off the volume, and the only one the data carriage may write. These tests
// pin the two things that makes safe: the write is atomic and complete, and
// the endpoint refuses a document that would silently do nothing.

func itoa(i int) string { return strconv.Itoa(i) }

// --- the edit screen ----------------------------------------------------------

func TestLodgingSaveWritesOverlay(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	cp := firstCheckpoint(t, legs)

	form := url.Values{
		"address-" + itoa(cp.Ord): {"1 Example Ridge Loop"},
		"notes-" + itoa(cp.Ord):   {"cabin behind the store"},
	}
	req := httptest.NewRequest("POST", "/admin/lodging", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	app.lodgingSave(w, req)

	if w.Code != 303 {
		t.Fatalf("status %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); strings.Contains(loc, "error=") {
		t.Fatalf("save rejected: %s", loc)
	}

	pd, err := loadPrivate(app.dataDir)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	got := pd.forRide(app.rideSlug, app.rideSlug).byOrd()[cp.Ord]
	if got.Address != "1 Example Ridge Loop" {
		t.Errorf("address = %q", got.Address)
	}
	if got.Notes != "cabin behind the store" {
		t.Errorf("notes = %q", got.Notes)
	}
}

// A checkpoint left blank on the form is an entry deleted — that is how a
// stale address goes away, and the form carries every checkpoint so the
// document it produces is complete.
func TestLodgingSaveBlankRemovesEntry(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	cp := firstCheckpoint(t, legs)

	if err := savePrivate(app.dataDir, privateData{Checkpoints: map[string]privateCheckpoint{
		itoa(cp.Ord): {Address: "somewhere stale"},
	}}); err != nil {
		t.Fatalf("seed overlay: %v", err)
	}

	req := httptest.NewRequest("POST", "/admin/lodging",
		strings.NewReader(url.Values{"address-" + itoa(cp.Ord): {"  "}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	app.lodgingSave(httptest.NewRecorder(), req)

	pd, _ := loadPrivate(app.dataDir)
	if _, ok := pd.Checkpoints[itoa(cp.Ord)]; ok {
		t.Errorf("entry survived a blank save: %+v", pd.Checkpoints)
	}
}

func TestLodgingFormRenders(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	cp := firstCheckpoint(t, legs)
	if err := savePrivate(app.dataDir, privateData{Checkpoints: map[string]privateCheckpoint{
		itoa(cp.Ord): {Address: "1 Example Ridge Loop", Notes: "truly a dump"},
	}}); err != nil {
		t.Fatalf("seed overlay: %v", err)
	}

	w := httptest.NewRecorder()
	app.lodgingForm(w, httptest.NewRequest("GET", "/admin/lodging", nil))
	if w.Code != 200 {
		t.Fatalf("status %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"1 Example Ridge Loop", "truly a dump",
		`name="address-` + itoa(cp.Ord), `name="notes-` + itoa(cp.Ord)} {
		if !strings.Contains(body, want) {
			t.Errorf("form missing %q", want)
		}
	}
}

// --- the carriage endpoint ----------------------------------------------------

func pushOverlay(t *testing.T, app *App, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/admin/private", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.privatePush(w, req)
	return w
}

func TestPrivatePushStoresAndReportsCount(t *testing.T) {
	app := testApp(t)
	w := pushOverlay(t, app, `{"checkpoints":{"3":{"address":"1 Example Ridge Loop"},
	  "4":{"address":"Canyons Edge Motel","notes":"truly a dump"}}}`)

	if w.Code != 200 {
		t.Fatalf("status %d, want 200: %s", w.Code, w.Body.String())
	}
	var out struct {
		Status      string `json:"status"`
		Checkpoints int    `json:"checkpoints"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, w.Body.String())
	}
	// The workflow compares this count against what it sent; if it stops being
	// the real number the carriage's only integrity check goes quiet.
	if out.Status != "ok" || out.Checkpoints != 2 {
		t.Errorf("response = %+v, want ok/2", out)
	}

	pd, _ := loadPrivate(app.dataDir)
	if pd.forRide(app.rideSlug, app.rideSlug).byOrd()[4].Notes != "truly a dump" {
		t.Errorf("overlay did not land: %+v", pd.Checkpoints)
	}
}

// A key that is not an ord would be accepted and then silently dropped by
// byOrd. Refuse it while the pusher is still watching.
func TestPrivatePushRejectsNonOrdKey(t *testing.T) {
	app := testApp(t)
	w := pushOverlay(t, app, `{"checkpoints":{"cloudcroft":{"address":"x"}}}`)
	if w.Code != 400 {
		t.Errorf("status %d, want 400", w.Code)
	}
	if _, err := os.Stat(filepath.Join(app.dataDir, privateFile)); !os.IsNotExist(err) {
		t.Error("a rejected push still wrote the overlay")
	}
}

func TestPrivatePushRejectsBadJSON(t *testing.T) {
	app := testApp(t)
	if w := pushOverlay(t, app, `{"checkpoints":`); w.Code != 400 {
		t.Errorf("status %d, want 400", w.Code)
	}
}

// A push replaces the document wholesale — that is what makes the secret the
// canonical copy rather than one contributor to a merge.
func TestPrivatePushReplacesWholeDocument(t *testing.T) {
	app := testApp(t)
	if err := savePrivate(app.dataDir, privateData{Checkpoints: map[string]privateCheckpoint{
		"1": {Address: "old"}, "2": {Address: "also old"},
	}}); err != nil {
		t.Fatalf("seed overlay: %v", err)
	}
	pushOverlay(t, app, `{"checkpoints":{"3":{"address":"new"}}}`)

	pd, _ := loadPrivate(app.dataDir)
	if len(pd.Checkpoints) != 1 || pd.forRide(app.rideSlug, app.rideSlug).byOrd()[3].Address != "new" {
		t.Errorf("push did not replace: %+v", pd.Checkpoints)
	}
}

// --- the write itself ---------------------------------------------------------

// The overlay is re-read on every admin request, so the write must never be
// observable half-done, and it must not leave its temp files behind.
func TestSavePrivateIsAtomicAndTidy(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := savePrivate(dir, privateData{Checkpoints: map[string]privateCheckpoint{
			"1": {Address: "round " + itoa(i)},
		}}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".private-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want just the overlay", len(entries))
	}

	fi, err := os.Stat(filepath.Join(dir, privateFile))
	if err != nil {
		t.Fatal(err)
	}
	// The addresses live in this file.
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
	pd, _ := loadPrivate(dir)
	if pd.forRide("first", "first").byOrd()[1].Address != "round 2" {
		t.Errorf("last write did not win: %+v", pd.Checkpoints)
	}
}

// An absent overlay is the normal state for anyone who clones and runs this,
// not an error.
func TestLoadPrivateAbsentIsNotAnError(t *testing.T) {
	pd, err := loadPrivate(t.TempDir())
	if err != nil {
		t.Fatalf("absent overlay errored: %v", err)
	}
	if len(pd.forRide("first", "first").byOrd()) != 0 {
		t.Errorf("want empty, got %+v", pd.Checkpoints)
	}
}

// --- the overlay is per ride ----------------------------------------------------

// overlayOnDisk reads the overlay file as it is, so a test can say "this
// block did not change by a byte" without trusting the code under test.
func overlayOnDisk(t *testing.T, app *App) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(app.dataDir, privateFile))
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("overlay is not JSON: %v", err)
	}
	return out
}

// The file the site wrote when it held one ride, keyed by ord with no ride in
// it, still reads — as the FIRST ride's, and as no other ride's. Shown a
// second ride, the signed-in screens carry none of it.
func TestOverlayOldShapeBelongsToTheFirstRideOnly(t *testing.T) {
	app := activeTripApp(t) // the fixture trip is showing; app.rideSlug is the first ride
	const first1, first2 = "OVERLAY-FIRST-RIDE 1 Example Ridge Loop", "OVERLAY-FIRST-RIDE note for stop two"
	old := `{"checkpoints": {"1": {"address": "` + first1 + `", "notes": ""}, "2": {"address": "", "notes": "` + first2 + `"}}}`
	if err := os.WriteFile(filepath.Join(app.dataDir, privateFile), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	screens := []string{"/admin/post", "/admin/lodging"}

	// The second ride is showing: its checkpoints 1 and 2 are not the first
	// ride's, and the first ride's text is on neither screen.
	for _, p := range screens {
		w := send(t, app, "GET", p, testToken, "", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Alder") {
			t.Fatalf("%s: status %d, or not the fixture ride's screen", p, w.Code)
		}
		if strings.Contains(w.Body.String(), "OVERLAY-FIRST-RIDE") {
			t.Errorf("%s shows the first ride's private text against the second ride's checkpoints", p)
		}
	}
	// The first ride is showing again: the old shape is its overlay, as before.
	if w := postForm(t, app, "/admin/ride/active", url.Values{"clear": {"1"}}); w.Code != 200 {
		t.Fatalf("clear the active ride: %d", w.Code)
	}
	lodging := send(t, app, "GET", "/admin/lodging", testToken, "", nil).Body.String()
	if !strings.Contains(lodging, first1) || !strings.Contains(lodging, first2) {
		t.Error("the old overlay shape no longer reads as the first ride's")
	}
	if post := send(t, app, "GET", "/admin/post", testToken, "", nil).Body.String(); !strings.Contains(post, first1) {
		t.Error("the post form lost the first ride's address")
	}
}

// One ride's private text never renders on another, in either direction, and
// saving one ride's lodging form leaves the other ride's block byte-identical.
func TestOverlayOneRidesTextNeverRendersOnAnother(t *testing.T) {
	app := activeTripApp(t)
	const firstText, secondText = "OVERLAY-FIRST-RIDE 1 Example Ridge Loop", "OVERLAY-SECOND-RIDE 9 Invented Dock Road"
	if err := savePrivate(app.dataDir, privateData{Checkpoints: map[string]privateCheckpoint{
		"1": {Address: firstText, Notes: "OVERLAY-FIRST-RIDE gate code"}, "3": {Address: "OVERLAY-FIRST-RIDE cabin"},
	}}); err != nil {
		t.Fatal(err)
	}
	firstBlock := string(overlayOnDisk(t, app)["checkpoints"])

	// Save the lodging form while the SECOND ride is showing.
	if w := postForm(t, app, "/admin/lodging", url.Values{"address-1": {secondText}, "notes-2": {"OVERLAY-SECOND-RIDE side door"}}); w.Code != 303 ||
		strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatalf("save lodging on the second ride: status %d, Location %q", w.Code, w.Header().Get("Location"))
	}
	disk := overlayOnDisk(t, app)
	if got := string(disk["checkpoints"]); got != firstBlock {
		t.Errorf("saving the second ride's lodging changed the first ride's block:\n before %s\n after  %s", firstBlock, got)
	}
	pd, err := loadPrivate(app.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	second := pd.forRide(fixtureSlug, app.rideSlug).byOrd()
	if second[1].Address != secondText || second[2].Notes != "OVERLAY-SECOND-RIDE side door" || len(second) != 2 {
		t.Errorf("the second ride's block: %+v", second)
	}
	if first := pd.forRide(app.rideSlug, app.rideSlug).byOrd(); first[1].Address != firstText || len(first) != 2 {
		t.Errorf("the first ride's block: %+v", first)
	}
	if other := pd.forRide("some-third-ride", app.rideSlug).byOrd(); len(other) != 0 {
		t.Errorf("a ride with no block was handed %+v", other)
	}

	screens := []string{"/admin/post", "/admin/lodging"}
	for _, p := range screens {
		body := send(t, app, "GET", p, testToken, "", nil).Body.String()
		if !strings.Contains(body, secondText) {
			t.Errorf("%s on the second ride lacks its own address", p)
		}
		if strings.Contains(body, "OVERLAY-FIRST-RIDE") {
			t.Errorf("%s on the second ride shows the first ride's private text", p)
		}
	}
	if w := postForm(t, app, "/admin/ride/active", url.Values{"clear": {"1"}}); w.Code != 200 {
		t.Fatalf("clear the active ride: %d", w.Code)
	}
	for _, p := range screens {
		body := send(t, app, "GET", p, testToken, "", nil).Body.String()
		if !strings.Contains(body, firstText) {
			t.Errorf("%s on the first ride lacks its own address", p)
		}
		if strings.Contains(body, "OVERLAY-SECOND-RIDE") {
			t.Errorf("%s on the first ride shows the second ride's private text", p)
		}
	}
	// Saving the FIRST ride's form leaves the second ride's block alone, and
	// keeps the first ride's block where the carriage has always put it.
	secondBlock := string(disk["rides"])
	if w := postForm(t, app, "/admin/lodging", url.Values{"address-1": {"OVERLAY-FIRST-RIDE moved"}}); w.Code != 303 {
		t.Fatalf("save lodging on the first ride: %d", w.Code)
	}
	disk = overlayOnDisk(t, app)
	if got := string(disk["rides"]); got != secondBlock {
		t.Errorf("saving the first ride's lodging changed the second ride's block:\n before %s\n after  %s", secondBlock, got)
	}
	if !strings.Contains(string(disk["checkpoints"]), "OVERLAY-FIRST-RIDE moved") {
		t.Error("the first ride's block is no longer the top-level checkpoints")
	}
	// No anonymous page has any of it, for either ride.
	for _, p := range []string{"/", "/plan", "/route.geojson", "/photos", "/checklist"} {
		if body := send(t, app, "GET", p, "", "", nil).Body.String(); strings.Contains(body, "OVERLAY-") {
			t.Errorf("%s carries overlay text", p)
		}
	}
}

// The carriage: the shape it has always sent replaces the first ride's block
// and nothing else; ?ride= addresses another ride; a document with rides is
// the whole overlay.
func TestPrivatePushIsPerRide(t *testing.T) {
	app := activeTripApp(t)
	push := func(query, body string) *httptest.ResponseRecorder {
		return send(t, app, "POST", "/admin/private"+query, testToken, "application/json", []byte(body))
	}
	if w := push("?ride="+fixtureSlug, `{"checkpoints":{"2":{"address":"OVERLAY-SECOND-RIDE dock"}}}`); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"checkpoints":1`) {
		t.Fatalf("push one ride's block: %d %s", w.Code, w.Body.String())
	}
	// The old shape, as the Data workflow sends it: the first ride's, and the
	// second ride's block survives it.
	if w := push("", `{"checkpoints":{"3":{"address":"OVERLAY-FIRST-RIDE cabin"},"4":{"address":"x"}}}`); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"checkpoints":2`) {
		t.Fatalf("push the old shape: %d %s", w.Code, w.Body.String())
	}
	pd, _ := loadPrivate(app.dataDir)
	if got := pd.forRide(fixtureSlug, app.rideSlug).byOrd(); got[2].Address != "OVERLAY-SECOND-RIDE dock" || len(got) != 1 {
		t.Errorf("the old-shape push reached the second ride's block: %+v", got)
	}
	if got := pd.forRide(app.rideSlug, app.rideSlug).byOrd(); got[3].Address != "OVERLAY-FIRST-RIDE cabin" || len(got) != 2 {
		t.Errorf("the first ride's block after the old-shape push: %+v", got)
	}
	// Refusals, each leaving the file as it is.
	before, _ := os.ReadFile(filepath.Join(app.dataDir, privateFile))
	for name, c := range map[string][2]string{
		"a ride that is not in the database": {"?ride=no-such-ride", `{"checkpoints":{"1":{"address":"x"}}}`},
		"a ride that is a path":              {"?ride=../escape", `{"checkpoints":{"1":{"address":"x"}}}`},
		"?ride= with a whole document":       {"?ride=" + fixtureSlug, `{"rides":{}}`},
		"a rides key that is a path":         {"", `{"rides":{"../escape":{"checkpoints":{}}}}`},
		"an ord that is not one, in a ride":  {"", `{"rides":{"` + fixtureSlug + `":{"checkpoints":{"dock":{"address":"x"}}}}}`},
		"the first ride given twice":         {"", `{"checkpoints":{"1":{"address":"x"}},"rides":{"` + app.rideSlug + `":{"checkpoints":{"1":{"address":"y"}}}}}`},
	} {
		if w := push(c[0], c[1]); w.Code < 400 || w.Code >= 500 {
			t.Errorf("%s: status %d, want a 4xx", name, w.Code)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(app.dataDir, privateFile)); string(after) != string(before) {
		t.Error("a refused push changed the overlay")
	}
	// A document with rides replaces everything.
	if w := push("", `{"checkpoints":{"1":{"address":"OVERLAY-FIRST-RIDE only"}},"rides":{"`+fixtureSlug+`":{"checkpoints":{"5":{"notes":"OVERLAY-SECOND-RIDE only"}}}}}`); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"checkpoints":2`) {
		t.Fatalf("push a whole document: %d %s", w.Code, w.Body.String())
	}
	pd, _ = loadPrivate(app.dataDir)
	if a, b := pd.forRide(app.rideSlug, app.rideSlug).byOrd(), pd.forRide(fixtureSlug, app.rideSlug).byOrd(); len(a) != 1 || len(b) != 1 ||
		a[1].Address != "OVERLAY-FIRST-RIDE only" || b[5].Notes != "OVERLAY-SECOND-RIDE only" {
		t.Errorf("after a whole-document push: first %+v, second %+v", a, b)
	}
}
