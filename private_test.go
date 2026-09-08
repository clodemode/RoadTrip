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
	got := pd.byOrd()[cp.Ord]
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
	if pd.byOrd()[4].Notes != "truly a dump" {
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
	if len(pd.Checkpoints) != 1 || pd.byOrd()[3].Address != "new" {
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
	if pd.byOrd()[1].Address != "round 2" {
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
	if len(pd.byOrd()) != 0 {
		t.Errorf("want empty, got %+v", pd.Checkpoints)
	}
}
