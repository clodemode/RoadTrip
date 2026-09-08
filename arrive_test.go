package main

import (
	"encoding/json"
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

// stageSeed writes the embedded ride file out where RefreshPlanning can read
// it — the same move main() makes on every boot, which is when a seed edit
// reaches a running site.
func stageSeed(t *testing.T, app *App) string {
	t.Helper()
	names, err := fs.Glob(seedFS, "seed/*.geojson")
	if err != nil || len(names) == 0 {
		t.Fatalf("no embedded seed: %v", err)
	}
	raw, err := seedFS.ReadFile(names[0])
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "staged.geojson")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("stage seed: %v", err)
	}
	return path
}

// "Made it to" is the moment the plan stops being the record: an arrival with
// a location overwrites the checkpoint's guessed point with where the car
// actually stopped, and the lines that ran to the guess follow it. The one
// thing it must never do is write down a raw fix at a private place, so a
// fuzzed checkpoint gets the stamp and keeps its grid cell.

// arrive posts to the arrive route the way the form and curl both do.
func arrive(t *testing.T, app *App, id int64, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	target := "/admin/checkpoint/" + strconv.FormatInt(id, 10) + "/arrive"
	req := httptest.NewRequest("POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	w := httptest.NewRecorder()
	app.checkpointArrive(w, req)
	return w
}

func mustCheckpoint(t *testing.T, app *App, id int64) *Checkpoint {
	t.Helper()
	c, err := app.store.Checkpoint(id)
	if err != nil {
		t.Fatalf("read back checkpoint %d: %v", id, err)
	}
	return c
}

// findCheckpoint returns the ride's first checkpoint with the given precision.
func findCheckpoint(t *testing.T, legs []Leg, precision string) *Checkpoint {
	t.Helper()
	for i := range legs {
		for j := range legs[i].Checkpoints {
			if legs[i].Checkpoints[j].Precision == precision {
				return &legs[i].Checkpoints[j]
			}
		}
	}
	t.Fatalf("fixture has no %s checkpoint", precision)
	return nil
}

// The headline: the lodging post IS the fix, and adopting its published pin
// moves a public checkpoint off the town centroid the plan guessed at.
func TestArriveWithPostIDMovesAPublicCheckpoint(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	cp := findCheckpoint(t, legs, precisionApprox)

	lat, lng := 35.2266, -112.4811
	p := mustCreate(t, app, &Post{LegID: cp.LegID, CheckpointID: cp.ID, Kind: "lodging",
		TZ: cp.TZ, Title: "the motel", Lat: &lat, Lng: &lng, PinSource: pinEXIF})

	if w := arrive(t, app, cp.ID, url.Values{"post_id": {strconv.FormatInt(p.ID, 10)}}); w.Code != 303 {
		t.Fatalf("status %d, want 303: %s", w.Code, w.Body.String())
	}

	got := mustCheckpoint(t, app, cp.ID)
	if got.ArrivedAt == "" {
		t.Error("no arrival stamp")
	}
	if got.Lat != lat || got.Lng != lng {
		t.Errorf("checkpoint did not move: %v,%v want %v,%v", got.Lat, got.Lng, lat, lng)
	}
	// A public place that has been arrived at is no longer a guess.
	if got.Precision != precisionExact {
		t.Errorf("precision = %q, want exact", got.Precision)
	}
}

// A private place gets the stamp and keeps the cell. The raw fix must not be
// on the volume at all — not merely hidden on the way out — so this reads the
// column directly rather than through scanCheckpoint's snap.
func TestArriveKeepsAFuzzedCheckpointSnapped(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	cp := findCheckpoint(t, legs, precisionFuzzed)

	lat, lng := cp.Lat+0.0123, cp.Lng-0.0111
	p := mustCreate(t, app, &Post{LegID: cp.LegID, CheckpointID: cp.ID, Kind: "lodging",
		TZ: cp.TZ, Title: "home", Lat: &lat, Lng: &lng, PinSource: pinBrowser})

	if w := arrive(t, app, cp.ID, url.Values{"post_id": {strconv.FormatInt(p.ID, 10)}}); w.Code != 303 {
		t.Fatalf("status %d, want 303", w.Code)
	}

	got := mustCheckpoint(t, app, cp.ID)
	if got.Precision != precisionFuzzed {
		t.Errorf("precision = %q, want it left fuzzed", got.Precision)
	}
	var rawLat, rawLng float64
	if err := app.store.db.QueryRow(`SELECT lat,lng FROM checkpoints WHERE id=?`, cp.ID).
		Scan(&rawLat, &rawLng); err != nil {
		t.Fatalf("read the stored point: %v", err)
	}
	if rawLat != snap(lat) || rawLng != snap(lng) {
		t.Errorf("stored point = %v,%v, want the snapped %v,%v", rawLat, rawLng, snap(lat), snap(lng))
	}
	if rawLat == lat || rawLng == lng {
		t.Error("the raw fix at a private place was written to the volume")
	}
}

// An explicit point, for a stop nobody photographed.
func TestArriveWithLatLng(t *testing.T) {
	app := testApp(t)
	cp := findCheckpoint(t, testLegs(t, app), precisionApprox)

	if w := arrive(t, app, cp.ID, url.Values{"lat": {"35.2266"}, "lng": {"-112.4811"}}); w.Code != 303 {
		t.Fatalf("status %d, want 303", w.Code)
	}
	got := mustCheckpoint(t, app, cp.ID)
	if got.Lat != 35.2266 || got.Lng != -112.4811 {
		t.Errorf("point = %v,%v, want 35.2266,-112.4811", got.Lat, got.Lng)
	}
}

// Half a coordinate is a mistake worth refusing while the pusher is watching,
// not a silent stamp at the old place.
func TestArriveRejectsHalfACoordinate(t *testing.T) {
	app := testApp(t)
	cp := findCheckpoint(t, testLegs(t, app), precisionApprox)

	if w := arrive(t, app, cp.ID, url.Values{"lat": {"35.2266"}}); w.Code != 400 {
		t.Errorf("status %d, want 400", w.Code)
	}
	if got := mustCheckpoint(t, app, cp.ID); got.ArrivedAt != "" {
		t.Error("a refused arrival still stamped the checkpoint")
	}
}

// A post with no published pin has nothing to adopt.
func TestArriveRejectsAPinlessPost(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	cp := findCheckpoint(t, legs, precisionApprox)
	p := mustCreate(t, app, &Post{LegID: cp.LegID, Kind: "note", TZ: cp.TZ, Title: "no pin"})

	if w := arrive(t, app, cp.ID, url.Values{"post_id": {strconv.FormatInt(p.ID, 10)}}); w.Code != 400 {
		t.Errorf("status %d, want 400", w.Code)
	}
	if got := mustCheckpoint(t, app, cp.ID); got.ArrivedAt != "" {
		t.Error("a refused arrival still stamped the checkpoint")
	}
}

// The stamp can be a mis-tap; where the car stopped is a fact of its own. So
// clearing takes the time back and leaves the location.
func TestArriveClearKeepsTheLocation(t *testing.T) {
	app := testApp(t)
	cp := findCheckpoint(t, testLegs(t, app), precisionApprox)

	arrive(t, app, cp.ID, url.Values{"lat": {"35.2266"}, "lng": {"-112.4811"}})
	if w := arrive(t, app, cp.ID, url.Values{"clear": {"1"}}); w.Code != 303 {
		t.Fatalf("status %d, want 303", w.Code)
	}

	got := mustCheckpoint(t, app, cp.ID)
	if got.ArrivedAt != "" {
		t.Errorf("arrived_at = %q, want it cleared", got.ArrivedAt)
	}
	if got.Lat != 35.2266 || got.Lng != -112.4811 {
		t.Errorf("clearing moved the checkpoint back to %v,%v", got.Lat, got.Lng)
	}
	if got.Precision != precisionExact {
		t.Errorf("precision = %q, want exact — the place is still known", got.Precision)
	}
}

// --- a stamp other than now ------------------------------------------------

// at exists for one job: putting back the real arrival time after a repair.
// Clearing a stamp is the only way to hand a checkpoint back to the seed, and
// without this the re-stamp would overwrite the arrival with the clock of the
// repair — losing the one fact the stamp was there to hold.
func TestArriveAtStampsTheGivenTime(t *testing.T) {
	app := testApp(t)
	cp := findCheckpoint(t, testLegs(t, app), precisionApprox)

	// An offset, not Z: this is what a person reads off their own clock, and
	// the store keeps UTC.
	if w := arrive(t, app, cp.ID, url.Values{"at": {"2026-09-06T20:06:47-07:00"}}); w.Code != 303 {
		t.Fatalf("status %d, want 303: %s", w.Code, w.Body.String())
	}
	got := mustCheckpoint(t, app, cp.ID)
	want := time.Date(2026, 9, 7, 3, 6, 47, 0, time.UTC)
	at, ok := parseUTC(got.ArrivedAt)
	if !ok {
		t.Fatalf("unparsable arrived_at %q", got.ArrivedAt)
	}
	if !at.Equal(want) {
		t.Errorf("arrived_at = %s, want %s", at.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// The repair in full: a stamped checkpoint is frozen against the seed, so
// clear it, let the seed refresh it, then stamp it back with its own time.
// The name comes back and the arrival is unchanged.
func TestArriveAtRestoresAfterASeedRepair(t *testing.T) {
	app := testApp(t)
	cp := findCheckpoint(t, testLegs(t, app), precisionApprox)
	original := "2026-09-06T20:06:47-07:00"

	// Stamped and moved, the way the road leaves it.
	arrive(t, app, cp.ID, url.Values{"at": {original}, "lat": {"37.9"}, "lng": {"-119.1"}})
	staged := stageSeed(t, app)
	if _, err := app.store.RefreshPlanning(staged, app.rideSlug); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := mustCheckpoint(t, app, cp.ID); got.Lat != 37.9 {
		t.Fatalf("precondition: the seed took the point back while stamped")
	}

	// The repair.
	arrive(t, app, cp.ID, url.Values{"clear": {"1"}})
	if _, err := app.store.RefreshPlanning(staged, app.rideSlug); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := mustCheckpoint(t, app, cp.ID); got.Lat != cp.Lat || got.Name != cp.Name {
		t.Errorf("the seed did not retake the cleared checkpoint: %+v", got)
	}
	arrive(t, app, cp.ID, url.Values{"at": {original}})

	got := mustCheckpoint(t, app, cp.ID)
	want := time.Date(2026, 9, 7, 3, 6, 47, 0, time.UTC)
	at, _ := parseUTC(got.ArrivedAt)
	if !at.Equal(want) {
		t.Errorf("arrived_at = %q, want the original %s", got.ArrivedAt, want.Format(time.RFC3339))
	}
	if got.Name != cp.Name {
		t.Errorf("name = %q, want the seed's %q", got.Name, cp.Name)
	}
}

// at travels with a location like any other stamp.
func TestArriveAtCarriesALocation(t *testing.T) {
	app := testApp(t)
	cp := findCheckpoint(t, testLegs(t, app), precisionApprox)

	arrive(t, app, cp.ID, url.Values{
		"at": {"2026-09-06T20:06:47-07:00"}, "lat": {"38.2571"}, "lng": {"-119.2308"}})

	got := mustCheckpoint(t, app, cp.ID)
	if got.Lat != 38.2571 || got.Lng != -119.2308 {
		t.Errorf("point = %v,%v, want the given one", got.Lat, got.Lng)
	}
	if got.Precision != precisionExact {
		t.Errorf("precision = %q, want exact", got.Precision)
	}
	if at, _ := parseUTC(got.ArrivedAt); !at.Equal(time.Date(2026, 9, 7, 3, 6, 47, 0, time.UTC)) {
		t.Errorf("arrived_at = %q, want the supplied time", got.ArrivedAt)
	}
}

// Every way of getting it wrong is refused while the pusher is watching,
// rather than stamping something that reads plausible and is not.
func TestArriveAtRefusesWhatWouldGoWrongQuietly(t *testing.T) {
	cases := []struct {
		name string
		form url.Values
	}{
		// A bare wall clock has no offset, so it would silently mean UTC.
		{"no offset", url.Values{"at": {"2026-09-06T20:06:47"}}},
		{"not a time", url.Values{"at": {"yesterday evening"}}},
		// The zero time is how the store spells a cleared stamp: this would
		// un-stamp the checkpoint while looking like a stamp.
		{"zero time", url.Values{"at": {"0001-01-01T00:00:00Z"}}},
		// defaultTZ follows the latest arrival, so a mistyped year would own
		// the post form's zone for the rest of the ride.
		{"the future", url.Values{"at": {"2036-09-06T20:06:47-07:00"}}},
		// One stamps a time, the other removes it.
		{"at with clear", url.Values{"at": {"2026-09-06T20:06:47-07:00"}, "clear": {"1"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := testApp(t)
			cp := findCheckpoint(t, testLegs(t, app), precisionApprox)
			if w := arrive(t, app, cp.ID, c.form); w.Code != 400 {
				t.Errorf("status %d, want 400", w.Code)
			}
			if got := mustCheckpoint(t, app, cp.ID); got.ArrivedAt != "" {
				t.Errorf("a refused stamp still landed: %q", got.ArrivedAt)
			}
		})
	}
}

// No at is the everyday case — the button — and it still means now.
func TestArriveWithoutAtStampsNow(t *testing.T) {
	app := testApp(t)
	cp := findCheckpoint(t, testLegs(t, app), precisionApprox)
	before := time.Now().Add(-time.Second)

	arrive(t, app, cp.ID, url.Values{})

	got := mustCheckpoint(t, app, cp.ID)
	at, ok := parseUTC(got.ArrivedAt)
	if !ok {
		t.Fatalf("unparsable arrived_at %q", got.ArrivedAt)
	}
	if at.Before(before) || at.After(time.Now().Add(time.Second)) {
		t.Errorf("arrived_at = %s, want about now", at.Format(time.RFC3339))
	}
}

// The plan's polyline ended at a guess. Once the day is stamped, the leg ends
// where the car did — and the next leg starts there, because the two share the
// boundary checkpoint.
func TestLegEndpointsFollowAnArrival(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	if len(legs) < 2 {
		t.Skip("needs two legs")
	}
	term := legTerminus(legs[0])
	if term == nil {
		t.Skip("leg 1 has no checkpoint")
	}

	lat, lng := term.Lat+0.02, term.Lng+0.02
	if err := app.store.SetArrived(term.ID, time.Now(), &LatLng{Lat: lat, Lng: lng}); err != nil {
		t.Fatalf("arrive: %v", err)
	}
	// Whatever the store publishes for this checkpoint is what both lines must
	// carry — a fuzzed one contributes its snapped point, not the raw fix.
	want := mustCheckpoint(t, app, term.ID)

	rv, err := app.loadRide()
	if err != nil {
		t.Fatalf("load ride: %v", err)
	}
	first, last := legVertices(t, rv.Legs[0])
	if last[0] != want.Lng || last[1] != want.Lat {
		t.Errorf("leg 1 ends at %v, want the arrival %v,%v", last, want.Lng, want.Lat)
	}
	if first[0] == want.Lng && first[1] == want.Lat {
		t.Error("leg 1's own start moved, and only its end should have")
	}
	next, _ := legVertices(t, rv.Legs[1])
	if next[0] != want.Lng || next[1] != want.Lat {
		t.Errorf("leg 2 starts at %v, want the arrival %v,%v", next, want.Lng, want.Lat)
	}
}

// An unstamped ride is the plan, untouched.
func TestLegEndpointsUnchangedWithoutArrivals(t *testing.T) {
	app := testApp(t)
	before, err := app.store.Legs(app.rideSlug)
	if err != nil {
		t.Fatalf("legs: %v", err)
	}
	rv, err := app.loadRide()
	if err != nil {
		t.Fatalf("load ride: %v", err)
	}
	for i := range before {
		if string(before[i].Line) != string(rv.Legs[i].Line) {
			t.Errorf("leg %d line was rewritten with nothing arrived", before[i].Ord)
		}
	}
}

// The seed owns planning data until it is actualized, and an arrival is what
// actualizes a checkpoint. A redeploy must not put the guessed point back.
func TestRefreshPlanningLeavesAnArrivedCheckpointAlone(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	cp := findCheckpoint(t, legs, precisionApprox)

	lat, lng := cp.Lat+0.03, cp.Lng-0.03
	if err := app.store.SetArrived(cp.ID, time.Now(), &LatLng{Lat: lat, Lng: lng}); err != nil {
		t.Fatalf("arrive: %v", err)
	}

	staged := stageSeed(t, app)
	if _, err := app.store.RefreshPlanning(staged, app.rideSlug); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got := mustCheckpoint(t, app, cp.ID)
	if got.Lat != lat || got.Lng != lng {
		t.Errorf("the seed took the point back: %v,%v want %v,%v", got.Lat, got.Lng, lat, lng)
	}
	if got.Precision != precisionExact {
		t.Errorf("precision = %q, want the arrival's exact to survive the refresh", got.Precision)
	}
	if got.ArrivedAt == "" {
		t.Error("the refresh cleared the arrival stamp")
	}
}

// --- the actual track ------------------------------------------------------

// A leg with an arrived origin, pinned posts and an arrived terminus is a
// track: origin, posts in the order they happened, terminus.
func TestTrackRunsOriginPostsTerminus(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	leg := legs[0]
	if len(leg.Checkpoints) < 2 {
		t.Skip("needs a leg with two checkpoints")
	}
	origin, term := &leg.Checkpoints[0], legTerminus(leg)

	// Two posts, created newest-first so the ordering is actually exercised —
	// Store.Posts hands them back in that order.
	late, early := 31.5, 31.0
	lng := -99.0
	mustCreate(t, app, &Post{LegID: leg.ID, Kind: "photo", TZ: leg.TZ, Title: "late",
		OccurredAt: utcString(time.Date(2026, 9, 4, 18, 0, 0, 0, time.UTC)),
		Lat:        &late, Lng: &lng, PinSource: pinEXIF})
	mustCreate(t, app, &Post{LegID: leg.ID, Kind: "photo", TZ: leg.TZ, Title: "early",
		OccurredAt: utcString(time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)),
		Lat:        &early, Lng: &lng, PinSource: pinEXIF})

	for _, id := range []int64{origin.ID, term.ID} {
		if err := app.store.SetArrived(id, time.Now(), nil); err != nil {
			t.Fatalf("arrive: %v", err)
		}
	}

	rv, err := app.loadRide()
	if err != nil {
		t.Fatalf("load ride: %v", err)
	}
	posts, err := app.store.Posts(app.rideSlug, 0)
	if err != nil {
		t.Fatalf("posts: %v", err)
	}
	pts := legTracks(rv.Legs, posts)[leg.ID]
	if len(pts) != 4 {
		t.Fatalf("track has %d points, want 4: %v", len(pts), pts)
	}
	if pts[1][1] != early || pts[2][1] != late {
		t.Errorf("posts are out of time order: %v", pts)
	}
	o, tm := rv.Legs[0].Checkpoints[0], legTerminus(rv.Legs[0])
	if pts[0][0] != o.Lng || pts[0][1] != o.Lat {
		t.Errorf("track starts at %v, want the origin %v,%v", pts[0], o.Lng, o.Lat)
	}
	if pts[3][0] != tm.Lng || pts[3][1] != tm.Lat {
		t.Errorf("track ends at %v, want the terminus %v,%v", pts[3], tm.Lng, tm.Lat)
	}
}

// One fix is a pin, not a track — and a one-point LineString is not GeoJSON.
func TestTrackNeedsTwoPoints(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	if tracks := legTracks(legs, nil); len(tracks) != 0 {
		t.Errorf("a ride with no arrivals and no pins produced %d track(s)", len(tracks))
	}

	term := legTerminus(legs[0])
	if err := app.store.SetArrived(term.ID, time.Now(), nil); err != nil {
		t.Fatalf("arrive: %v", err)
	}
	rv, _ := app.loadRide()
	// Leg 1 now has one point (its terminus) and leg 2 has one (its origin,
	// the same checkpoint). Neither is a track.
	if tracks := legTracks(rv.Legs, nil); len(tracks) != 0 {
		t.Errorf("a single arrival produced %d track(s): %v", len(tracks), tracks)
	}
}

// The GeoJSON is what curl reads: the track has to be in it, checkpoints have
// to carry the id the arrive route is keyed by, and an arrival says made_it.
func TestRouteGeoJSONCarriesTrackAndMadeIt(t *testing.T) {
	app := testApp(t)
	legs := testLegs(t, app)
	leg := legs[0]
	origin, term := &leg.Checkpoints[0], legTerminus(leg)
	for _, id := range []int64{origin.ID, term.ID} {
		if err := app.store.SetArrived(id, time.Now(), nil); err != nil {
			t.Fatalf("arrive: %v", err)
		}
	}

	w := httptest.NewRecorder()
	app.routeGeoJSON(w, httptest.NewRequest("GET", "/route.geojson", nil))
	if w.Code != 200 {
		t.Fatalf("status %d, want 200", w.Code)
	}
	var fc struct {
		Features []struct {
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &fc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}

	tracks, madeIt, ids := 0, 0, 0
	for _, f := range fc.Features {
		switch f.Properties["kind"] {
		case "track":
			tracks++
			if f.Geometry.Type != "LineString" {
				t.Errorf("track geometry is %q", f.Geometry.Type)
			}
		case "checkpoint":
			if _, ok := f.Properties["id"]; ok {
				ids++
			}
			if f.Properties["made_it"] == true {
				madeIt++
				if f.Properties["arrived_at"] == nil {
					t.Error("made_it without arrived_at")
				}
			}
		}
	}
	if tracks != 1 {
		t.Errorf("%d track feature(s), want 1", tracks)
	}
	if madeIt != 2 {
		t.Errorf("%d made_it checkpoint(s), want 2", madeIt)
	}
	if ids != len(rideCheckpoints(legs)) {
		t.Errorf("%d checkpoint(s) carry an id, want %d", ids, len(rideCheckpoints(legs)))
	}
}

// legVertices returns a leg's first and last polyline vertex.
func legVertices(t *testing.T, l Leg) (first, last []float64) {
	t.Helper()
	var pts [][]float64
	if err := json.Unmarshal(l.Line, &pts); err != nil || len(pts) < 2 {
		t.Fatalf("leg %d has no usable line: %v", l.Ord, err)
	}
	return pts[0], pts[len(pts)-1]
}
