package main

import (
	"html/template"
	"io/fs"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The edit screen is the one surface that can rewrite a post after it is up,
// and the thing it must never do is quietly undo a decision made elsewhere —
// a deliberate re-pin, a photo's own GPS, the zone a time was recorded in.
// These tests pin that boundary down.

// testApp builds an App on a throwaway SQLite file, seeded from the ride
// geojson the binary itself embeds, so the fixture and production agree by
// construction rather than by a copy that can drift.
func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	names, err := fs.Glob(seedFS, "seed/*.geojson")
	if err != nil || len(names) == 0 {
		t.Fatalf("no embedded seed: %v", err)
	}
	raw, err := seedFS.ReadFile(names[0])
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	staged := filepath.Join(dir, "seed.geojson")
	if err := os.WriteFile(staged, raw, 0o600); err != nil {
		t.Fatalf("stage seed: %v", err)
	}
	base := strings.TrimSuffix(filepath.Base(names[0]), filepath.Ext(names[0]))
	slug, _, err := store.Seed(staged, base)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	app := &App{
		store:        store,
		auth:         NewAuth(),
		dataDir:      dir,
		mediaDir:     filepath.Join(dir, "media"),
		rideSlug:     slug,
		pinPrecision: pinPrecisionSnap,
	}
	app.tmpl = template.Must(template.New("").Funcs(app.funcs()).ParseFS(templateFS, "templates/*.html"))
	return app
}

func testLegs(t *testing.T, app *App) []Leg {
	t.Helper()
	legs, err := app.store.Legs(app.rideSlug)
	if err != nil {
		t.Fatalf("legs: %v", err)
	}
	if len(legs) < 2 {
		t.Fatalf("fixture needs >= 2 legs, got %d", len(legs))
	}
	return legs
}

func mustCreate(t *testing.T, app *App, p *Post) *Post {
	t.Helper()
	if p.OccurredAt == "" {
		p.OccurredAt = utcString(time.Date(2026, 9, 5, 18, 0, 0, 0, time.UTC))
	}
	if p.PinSource == "" {
		p.PinSource = pinNone
	}
	if err := app.store.CreatePost(p); err != nil {
		t.Fatalf("create post: %v", err)
	}
	return p
}

func mustGet(t *testing.T, app *App, id int64) *Post {
	t.Helper()
	got, err := app.store.Post(id)
	if err != nil {
		t.Fatalf("read back post %d: %v", id, err)
	}
	return got
}

// submitEdit posts the edit form and returns the Location header. Handlers are
// called directly rather than through the mux: requireAuth is not what these
// tests are about, and PathValue is what the handler actually reads.
func submitEdit(t *testing.T, app *App, id int64, form url.Values) string {
	t.Helper()
	target := "/admin/post/" + strconv.FormatInt(id, 10) + "/edit"
	req := httptest.NewRequest("POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	w := httptest.NewRecorder()
	app.postEditSave(w, req)
	if w.Code != 303 {
		t.Fatalf("edit: status %d, want 303", w.Code)
	}
	return w.Header().Get("Location")
}

// editOK submits and insists the handler did not bounce back with an error.
func editOK(t *testing.T, app *App, id int64, form url.Values) {
	t.Helper()
	if loc := submitEdit(t, app, id, form); strings.Contains(loc, "error=") {
		t.Fatalf("edit rejected: %s", loc)
	}
}

// editRejected submits and insists it DID bounce, returning the message.
func editRejected(t *testing.T, app *App, id int64, form url.Values) string {
	t.Helper()
	loc := submitEdit(t, app, id, form)
	if !strings.Contains(loc, "error=") {
		t.Fatalf("edit accepted, want rejection: %s", loc)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	return u.Query().Get("error")
}

// The headline case: change what a post IS and which leg it belongs to.
func TestPostEditChangesKindAndLeg(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	p := mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "note", TZ: legs[0].TZ, Title: "gas stop"})

	editOK(t, app, p.ID, url.Values{
		"kind": {"meal"}, "leg_id": {strconv.FormatInt(legs[1].ID, 10)},
		"tz": {legs[1].TZ}, "title": {"gas stop"},
	})

	got := mustGet(t, app, p.ID)
	if got.Kind != "meal" {
		t.Errorf("kind = %q, want meal", got.Kind)
	}
	if got.LegID != legs[1].ID {
		t.Errorf("leg = %d, want %d", got.LegID, legs[1].ID)
	}
	if got.Title != "gas stop" {
		t.Errorf("title = %q, want it preserved", got.Title)
	}
}

// A checkpoint is the more specific fact: choosing one from another leg moves
// the post to that leg and adopts that checkpoint's zone, even when the leg
// select still names the old one.
func TestPostEditCheckpointCarriesLegAndZone(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)

	var target *Checkpoint
	var targetLeg *Leg
	for i := range legs {
		if legs[i].ID == legs[0].ID {
			continue
		}
		for j := range legs[i].Checkpoints {
			if legs[i].Checkpoints[j].TZ != legs[0].TZ {
				target, targetLeg = &legs[i].Checkpoints[j], &legs[i]
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		t.Skip("fixture has no checkpoint on another leg in another zone")
	}

	p := mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "note", TZ: legs[0].TZ, Title: "x"})

	// leg_id deliberately still names the ORIGINAL leg — the checkpoint wins.
	editOK(t, app, p.ID, url.Values{
		"kind": {"note"}, "leg_id": {strconv.FormatInt(legs[0].ID, 10)},
		"checkpoint_id": {strconv.FormatInt(target.ID, 10)}, "tz": {""}, "title": {"x"},
	})

	got := mustGet(t, app, p.ID)
	if got.LegID != targetLeg.ID {
		t.Errorf("leg = %d, want %d (the checkpoint's leg)", got.LegID, targetLeg.ID)
	}
	if got.CheckpointID != target.ID {
		t.Errorf("checkpoint = %d, want %d", got.CheckpointID, target.ID)
	}
	if got.TZ != target.TZ {
		t.Errorf("tz = %q, want %q (the checkpoint's zone)", got.TZ, target.TZ)
	}
}

// A pin that came FROM a checkpoint has to follow the checkpoint, or the post
// names one place and pins another.
func TestPostEditMovesCheckpointDerivedPin(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	from, to := findTwoCheckpoints(t, legs)

	lat, lng := from.Lat, from.Lng
	p := mustCreate(t, app, &Post{LegID: from.LegID, CheckpointID: from.ID, Kind: "note",
		TZ: from.TZ, Title: "x", Lat: &lat, Lng: &lng, PinSource: pinCheckpoint})

	editOK(t, app, p.ID, url.Values{
		"kind": {"note"}, "leg_id": {strconv.FormatInt(to.LegID, 10)},
		"checkpoint_id": {strconv.FormatInt(to.ID, 10)}, "tz": {to.TZ}, "title": {"x"},
	})

	got := mustGet(t, app, p.ID)
	if got.PinSource != pinCheckpoint {
		t.Fatalf("pin source = %q, want checkpoint", got.PinSource)
	}
	if got.Lat == nil || *got.Lat != to.Lat || *got.Lng != to.Lng {
		t.Errorf("pin did not follow the checkpoint: got %v,%v want %v,%v",
			deref(got.Lat), deref(got.Lng), to.Lat, to.Lng)
	}
}

// A photo's own GPS is a fact about where the photo was taken. Re-filing the
// post must not move it, and must not touch its fuzz posture either.
func TestPostEditLeavesExifPinAlone(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	from, to := findTwoCheckpoints(t, legs)

	lat, lng := 34.21, -118.5
	p := mustCreate(t, app, &Post{LegID: from.LegID, CheckpointID: from.ID, Kind: "photo",
		TZ: from.TZ, Title: "x", Lat: &lat, Lng: &lng, PinSource: pinEXIF, PinFuzzed: true})

	editOK(t, app, p.ID, url.Values{
		"kind": {"photo"}, "leg_id": {strconv.FormatInt(to.LegID, 10)},
		"checkpoint_id": {strconv.FormatInt(to.ID, 10)}, "tz": {to.TZ}, "title": {"x"},
	})

	got := mustGet(t, app, p.ID)
	if got.CheckpointID != to.ID {
		t.Errorf("checkpoint = %d, want %d — the filing should still move", got.CheckpointID, to.ID)
	}
	if got.PinSource != pinEXIF {
		t.Errorf("pin source = %q, want it left as exif", got.PinSource)
	}
	if got.Lat == nil || *got.Lat != lat || *got.Lng != lng {
		t.Errorf("exif pin moved: got %v,%v want %v,%v", deref(got.Lat), deref(got.Lng), lat, lng)
	}
	if !got.PinFuzzed {
		t.Error("fuzz posture was dropped by a metadata edit")
	}
}

// Clearing the checkpoint a pin was derived from leaves nothing to derive it
// from, so the pin goes rather than lingering on a place the post no longer
// claims.
func TestPostEditClearingCheckpointDropsDerivedPin(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	from, _ := findTwoCheckpoints(t, legs)

	lat, lng := from.Lat, from.Lng
	p := mustCreate(t, app, &Post{LegID: from.LegID, CheckpointID: from.ID, Kind: "note",
		TZ: from.TZ, Title: "x", Lat: &lat, Lng: &lng, PinSource: pinCheckpoint})

	editOK(t, app, p.ID, url.Values{
		"kind": {"note"}, "leg_id": {strconv.FormatInt(from.LegID, 10)},
		"checkpoint_id": {""}, "tz": {from.TZ}, "title": {"x"},
	})

	got := mustGet(t, app, p.ID)
	if got.CheckpointID != 0 {
		t.Errorf("checkpoint = %d, want it cleared", got.CheckpointID)
	}
	if got.PinSource != pinNone || got.PinLat != nil {
		t.Errorf("pin = %q %v, want none", got.PinSource, deref(got.PinLat))
	}
}

// Correcting only the zone re-stamps the same wall clock into the new zone —
// "that happened at 3 PM Mountain, not Central" — rather than shifting the
// instant that was already right.
func TestPostEditZoneRestampsWallClock(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	p := mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "note", TZ: "America/Chicago", Title: "x"})

	editOK(t, app, p.ID, url.Values{
		"kind": {"note"}, "leg_id": {strconv.FormatInt(legs[0].ID, 10)},
		"tz": {"America/Denver"}, "when": {"2026-09-05T15:00"}, "title": {"x"},
	})

	got := mustGet(t, app, p.ID)
	if got.TZ != "America/Denver" {
		t.Fatalf("tz = %q, want America/Denver", got.TZ)
	}
	// 15:00 MDT is 21:00 UTC.
	want := time.Date(2026, 9, 5, 21, 0, 0, 0, time.UTC)
	at, ok := parseUTC(got.OccurredAt)
	if !ok {
		t.Fatalf("unparsable occurred_at %q", got.OccurredAt)
	}
	if !at.Equal(want) {
		t.Errorf("occurred_at = %s, want %s", at.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// An unknown kind is refused rather than reset to "note": silently discarding
// the kind a post already had is worse than saying no.
func TestPostEditRejectsUnknownKind(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	p := mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "sight", TZ: legs[0].TZ, Title: "x"})

	msg := editRejected(t, app, p.ID, url.Values{
		"kind": {"banana"}, "leg_id": {strconv.FormatInt(legs[0].ID, 10)}, "title": {"x"},
	})
	if msg == "" {
		t.Error("rejection carried no message")
	}
	if got := mustGet(t, app, p.ID); got.Kind != "sight" {
		t.Errorf("kind = %q, want the original sight to survive a rejected edit", got.Kind)
	}
}

// A post with no photo still needs words, exactly as it did when it was made.
func TestPostEditRejectsEmptyTextlessPost(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	p := mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "note", TZ: legs[0].TZ, Title: "something"})

	editRejected(t, app, p.ID, url.Values{
		"kind": {"note"}, "leg_id": {strconv.FormatInt(legs[0].ID, 10)},
		"title": {""}, "body": {"  "},
	})
	if got := mustGet(t, app, p.ID); got.Title != "something" {
		t.Errorf("title = %q, want the original to survive", got.Title)
	}
}

// The form's carried return path is attacker-reachable, so it is a same-site
// path or it is the fallback.
func TestSafeBack(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/", "/"},
		{"/legs/cloudcroft-to-flagstaff", "/legs/cloudcroft-to-flagstaff"},
		{"/photos", "/photos"},
		{"", "/"},
		{"//evil.example.com", "/"},
		{"https://evil.example.com/x", "/"},
		{"evil.example.com", "/"},
		{"/a/../b", "/b"},
	}
	for _, c := range cases {
		if got := safeBack(c.in, "/"); got != c.want {
			t.Errorf("safeBack(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The edit template has to actually execute — a bad field reference is a 500
// on the road, and nothing else in the suite would catch it.
func TestPostEditFormRenders(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	cp := firstCheckpoint(t, legs)
	p := mustCreate(t, app, &Post{LegID: cp.LegID, CheckpointID: cp.ID, Kind: "meal",
		TZ: cp.TZ, Title: "brisket", Body: "worth the stop"})

	req := httptest.NewRequest("GET", "/admin/post/"+strconv.FormatInt(p.ID, 10)+"/edit", nil)
	req.SetPathValue("id", strconv.FormatInt(p.ID, 10))
	w := httptest.NewRecorder()
	app.postEditForm(w, req)

	if w.Code != 200 {
		t.Fatalf("status %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"brisket", "worth the stop", `name="kind"`, `name="leg_id"`,
		`name="checkpoint_id"`, `name="tz"`, `name="when"`} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered form is missing %q", want)
		}
	}
	// The zone select must offer the post's own zone, or saving would re-file it.
	if !strings.Contains(body, `value="`+cp.TZ+`"`) {
		t.Errorf("zone select is missing the post's own zone %q", cp.TZ)
	}
}

// A missing post is a 404, not a panic.
func TestPostEditFormUnknownPost(t *testing.T) {
	app := testApp(t)
	req := httptest.NewRequest("GET", "/admin/post/9999/edit", nil)
	req.SetPathValue("id", "9999")
	w := httptest.NewRecorder()
	app.postEditForm(w, req)
	if w.Code != 404 {
		t.Errorf("status %d, want 404", w.Code)
	}
}

func findTwoCheckpoints(t *testing.T, legs []Leg) (from, to *Checkpoint) {
	t.Helper()
	var all []*Checkpoint
	for i := range legs {
		for j := range legs[i].Checkpoints {
			all = append(all, &legs[i].Checkpoints[j])
		}
	}
	if len(all) < 2 {
		t.Fatalf("fixture needs >= 2 checkpoints, got %d", len(all))
	}
	return all[0], all[len(all)-1]
}

func firstCheckpoint(t *testing.T, legs []Leg) *Checkpoint {
	t.Helper()
	for i := range legs {
		if len(legs[i].Checkpoints) > 0 {
			return &legs[i].Checkpoints[0]
		}
	}
	t.Fatal("fixture has no checkpoints")
	return nil
}

func deref(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}
