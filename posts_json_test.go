package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// The census exists to answer a question /route.geojson cannot: what is
// actually in there? The GeoJSON is open data and drops a post with no pin, so
// a hidden or unpinned post — and the duplicate photo the hotspot posted
// twice — is invisible in it. These tests hold the census to "every post, with
// the photo's identity attached".

func readCensus(t *testing.T, app *App) (int, []censusPost) {
	t.Helper()
	w := httptest.NewRecorder()
	app.postsJSON(w, httptest.NewRequest("GET", "/driver/posts.json", nil))
	if w.Code != 200 {
		t.Fatalf("status %d, want 200: %s", w.Code, w.Body.String())
	}
	var out struct {
		Ride  string       `json:"ride"`
		Count int          `json:"count"`
		Posts []censusPost `json:"posts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, w.Body.String())
	}
	if out.Ride != app.rideSlug {
		t.Errorf("ride = %q, want %q", out.Ride, app.rideSlug)
	}
	if out.Count != len(out.Posts) {
		t.Errorf("count = %d but %d posts", out.Count, len(out.Posts))
	}
	return out.Count, out.Posts
}

func TestCensusListsEveryPostIncludingUnpinned(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	cp := firstCheckpoint(t, legs)

	lat, lng := 32.9, -105.7
	pinned := mustCreate(t, app, &Post{LegID: cp.LegID, CheckpointID: cp.ID, Kind: "photo",
		TZ: cp.TZ, Title: "pinned", Lat: &lat, Lng: &lng, PinSource: pinEXIF, PinFuzzed: true})
	unpinned := mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "note", TZ: legs[0].TZ,
		Title: "no pin at all"})
	hidden := mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "note", TZ: legs[0].TZ,
		Title: "hidden", HideLocation: true, Lat: &lat, Lng: &lng, PinSource: pinEXIF})

	count, posts := readCensus(t, app)
	if count != 3 {
		t.Fatalf("census carries %d post(s), want 3 — /route.geojson drops two of these", count)
	}
	byID := map[int64]censusPost{}
	for _, p := range posts {
		byID[p.ID] = p
	}
	if got := byID[pinned.ID]; got.Lat == nil || *got.Lat != lat || !got.PinFuzzed ||
		got.PinSource != pinEXIF || got.Checkpoint != cp.Name || got.CheckpointID != cp.ID ||
		got.Leg != legs[0].Ord {
		t.Errorf("pinned post came back wrong: %+v", got)
	}
	if got := byID[unpinned.ID]; got.Lat != nil || got.Lng != nil {
		t.Errorf("unpinned post carries a point: %+v", got)
	}
	// A hidden post is IN the census — it is a post, and it can be a duplicate —
	// but the published point it does not have must not appear.
	if got := byID[hidden.ID]; got.Lat != nil {
		t.Errorf("a hidden post published its point: %+v", got)
	}
}

// Duplicate-hunting is the job: the same photo twice has to be visible as the
// same photo id and the same sha256, with taken_at to catch near-misses.
func TestCensusCarriesPhotoIdentity(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)

	ph := &Photo{Path: "2026/09/abc123.jpg", ThumbPath: "2026/09/abc123-800.jpg",
		SHA256: "abc123def456", TakenAt: utcString(time.Date(2026, 9, 5, 17, 30, 0, 0, time.UTC)),
		Width: 4032, Height: 3024}
	if err := app.store.CreatePhoto(ph); err != nil {
		t.Fatalf("create photo: %v", err)
	}
	// The same photo row filed twice — exactly what a retried upload leaves
	// behind, since the media store is content-addressed.
	for i := 0; i < 2; i++ {
		mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "photo", TZ: legs[0].TZ,
			Title: "the same shot", PhotoID: ph.ID})
	}

	_, posts := readCensus(t, app)
	seen := map[string]int{}
	for _, p := range posts {
		if p.Photo == nil {
			t.Fatalf("post %d lost its photo", p.ID)
		}
		if p.Photo.SHA256 != ph.SHA256 || p.Photo.ID != ph.ID {
			t.Errorf("photo identity = %+v", p.Photo)
		}
		if p.Photo.Path != "/media/"+ph.Path || p.Photo.Thumb != "/media/"+ph.ThumbPath {
			t.Errorf("photo paths are not fetchable: %+v", p.Photo)
		}
		if p.Photo.Width != 4032 || p.Photo.Height != 3024 || p.Photo.TakenAt != ph.TakenAt {
			t.Errorf("photo detail = %+v", p.Photo)
		}
		seen[p.Photo.SHA256]++
	}
	if seen[ph.SHA256] != 2 {
		t.Errorf("the duplicate is not visible: %v", seen)
	}
}

// It is the signed-in census of every photo on the volume: never cached, and
// never handed to another origin the way the open GeoJSON is.
func TestCensusIsNotCachedOrShared(t *testing.T) {
	app := testApp(t)
	w := httptest.NewRecorder()
	app.postsJSON(w, httptest.NewRequest("GET", "/driver/posts.json", nil))

	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want none", got)
	}
}
