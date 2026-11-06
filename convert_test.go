package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureZones = "alder=America/Chicago,birch_gate=America/Chicago,cedar_camp=America/Chicago," +
	"dogwood=America/Denver,fir_hollow=America/Denver,elm=America/Denver"

func fixtureRoute(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "route-fixture.json"))
	if err != nil {
		t.Fatalf("read route fixture: %v", err)
	}
	return raw
}

func convertFixture(t *testing.T) []byte {
	t.Helper()
	out, err := convertRoute(fixtureRoute(t), convertOptions{Slug: "fixture-converted", Start: "2030-05-01", Zones: fixtureZones})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	return out
}

// The converter's output is a trip file the app itself loads, with the route
// document's options intact and the same arithmetic.
func TestConvertRouteMakesALoadableTrip(t *testing.T) {
	out := convertFixture(t)
	slug, err := checkTrip(out)
	if err != nil || slug != "fixture-converted" {
		t.Fatalf("the converted trip does not load: slug %q, %v", slug, err)
	}
	doc, err := publishPlan(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Options) != 2 || doc.AvgMPH != 60 || doc.LongDayHours != 9.5 {
		t.Errorf("options %d, avg %v, long-day mark %v", len(doc.Options), doc.AvgMPH, doc.LongDayHours)
	}
	def := doc.summarize(doc.choices(), doc.StartDate)
	if def.TotalMiles != 1320 || def.DayCount != 3 || !def.Days[0].Long || def.StartDate != "2030-05-01" {
		t.Errorf("default plan: %+v", def)
	}
	stop := doc.summarize(doc.choices(map[string]string{"finish": "stopover"}), doc.StartDate)
	if stop.TotalMiles != 1280 || stop.DayCount != 4 || len(stop.Checkpoints) != 6 {
		t.Errorf("stopover plan: %v mi, %d days, %d checkpoints", stop.TotalMiles, stop.DayCount, len(stop.Checkpoints))
	}

	// Checkpoints: what each stop IS decides its label, its precision and
	// whether a day ends there.
	type want struct {
		name, label, precision string
		terminal               bool
		leg                    int
	}
	check := func(got []planCP, wants []want) {
		t.Helper()
		if len(got) != len(wants) {
			t.Fatalf("%d checkpoints, want %d: %+v", len(got), len(wants), got)
		}
		for i, w := range wants {
			c := got[i]
			if c.Ord != i+1 || c.Name != w.name || c.Label != w.label || c.Precision != w.precision || c.Terminal != w.terminal || c.Leg != w.leg {
				t.Errorf("checkpoint %d is %+v, want %+v", i+1, c, w)
			}
		}
	}
	check(def.Checkpoints, []want{
		{"Alder", "Start", precisionFuzzed, true, 1},
		{"Birch Gate", "Border crossing", precisionApprox, false, 1},
		{"Cedar Camp", "Night 1", precisionApprox, true, 1},
		{"Dogwood", "Night 2", precisionApprox, true, 2},
		{"Elm", "Finish", precisionFuzzed, true, 3},
	})
	check(stop.Checkpoints, []want{
		{"Alder", "Start", precisionFuzzed, true, 1},
		{"Birch Gate", "Border crossing", precisionApprox, false, 1},
		{"Cedar Camp", "Night 1", precisionApprox, true, 1},
		{"Dogwood", "Night 2", precisionApprox, true, 2},
		{"Fir Hollow", "Night 3", precisionApprox, true, 3},
		{"Elm", "Finish", precisionFuzzed, true, 4},
	})
	if def.Checkpoints[3].TZ != "America/Denver" || def.Checkpoints[0].TZ != "America/Chicago" {
		t.Errorf("zones did not land on their stops: %+v", def.Checkpoints)
	}
}

// The route document writes [lat, lng]; GeoJSON is [lng, lat].
func TestConvertFlipsTheGeometry(t *testing.T) {
	var tf struct {
		Features []struct {
			Properties map[string]any `json:"properties"`
			Geometry   struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(convertFixture(t), &tf); err != nil {
		t.Fatal(err)
	}
	lines, points := 0, 0
	for _, f := range tf.Features {
		switch f.Geometry.Type {
		case "LineString":
			lines++
			var pts [][]float64
			if err := json.Unmarshal(f.Geometry.Coordinates, &pts); err != nil {
				t.Fatal(err)
			}
			for _, p := range pts {
				if p[0] > -90 || p[1] < 0 {
					t.Fatalf("leg %v has a point %v that is not [lng, lat]", f.Properties["slug"], p)
				}
			}
			if f.Properties["slug"] == "d1-ridge" && (pts[0][0] != -100.4321 || pts[0][1] != 40.1234) {
				t.Errorf("d1-ridge starts at %v", pts[0])
			}
		case "Point":
			points++
			var p []float64
			_ = json.Unmarshal(f.Geometry.Coordinates, &p)
			if p[0] > -90 || p[1] < 0 {
				t.Fatalf("checkpoint %v is at %v, not [lng, lat]", f.Properties["name"], p)
			}
		}
	}
	if lines != 6 || points != 7 {
		t.Errorf("%d legs and %d checkpoints; want 6 and 7", lines, points)
	}
}

// A planning note was written for one reader; a label is rendered to all.
func TestConvertDoesNotCarryPlanningNotes(t *testing.T) {
	in := fixtureRoute(t)
	notes := []string{"planning note", "private-tag", "room-tag", "optional stop"}
	for _, n := range notes {
		if !bytes.Contains(in, []byte(n)) {
			t.Fatalf("the route fixture does not contain %q; this test would pass on nothing", n)
		}
	}
	out := convertFixture(t)
	for _, n := range notes {
		if bytes.Contains(out, []byte(n)) {
			t.Errorf("the converted trip carries %q", n)
		}
	}
	// A via that is only a name on the planning map does not become a checkpoint.
	if bytes.Contains(out, []byte("Hazel")) {
		t.Error("a reference place became part of the trip")
	}
}

// No zone, no checkpoint: there is no ride-wide zone to fall back on.
func TestConvertRefusesAStopWithoutAZone(t *testing.T) {
	zones := strings.Replace(fixtureZones, "dogwood=America/Denver,", "", 1)
	_, err := convertRoute(fixtureRoute(t), convertOptions{Slug: "fixture-converted", Start: "2030-05-01", Zones: zones})
	if err == nil || !strings.Contains(err.Error(), `"dogwood" has no time zone`) {
		t.Fatalf("got %v; want a refusal naming the stop", err)
	}
	// With no zones at all it is the first stop that is named.
	_, err = convertRoute(fixtureRoute(t), convertOptions{Slug: "fixture-converted", Start: "2030-05-01"})
	if err == nil || !strings.Contains(err.Error(), "has no time zone") {
		t.Fatalf("got %v; want a refusal", err)
	}
	// A zone that is not a zone is no better than none.
	bad := strings.Replace(fixtureZones, "dogwood=America/Denver", "dogwood=Mountain", 1)
	_, err = convertRoute(fixtureRoute(t), convertOptions{Slug: "fixture-converted", Start: "2030-05-01", Zones: bad})
	if err == nil || !strings.Contains(err.Error(), "not an IANA zone") {
		t.Fatalf("got %v; want a refusal of the bad zone", err)
	}
	// A zone for a stop the document does not have is a typo, and says so.
	_, err = convertRoute(fixtureRoute(t), convertOptions{Slug: "fixture-converted", Start: "2030-05-01", Zones: fixtureZones + ",dogwod=America/Denver"})
	if err == nil || !strings.Contains(err.Error(), "dogwod") {
		t.Fatalf("got %v; want a refusal naming the unknown stop", err)
	}
}

// The zone can ride in the document instead of on the command line.
func TestConvertTakesAZoneFromTheStopItself(t *testing.T) {
	in := bytes.Replace(fixtureRoute(t), []byte(`"kind": "night", "note": "a planning note about the room"`),
		[]byte(`"kind": "night", "tz": "America/Phoenix", "note": "a planning note about the room"`), 1)
	zones := strings.Replace(fixtureZones, "dogwood=America/Denver,", "", 1)
	out, err := convertRoute(in, convertOptions{Slug: "fixture-converted", Start: "2030-05-01", Zones: zones})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !bytes.Contains(out, []byte("America/Phoenix")) {
		t.Error("the stop's own tz was not used")
	}
}

func TestConvertRefusesWhatItCannotSay(t *testing.T) {
	base := convertOptions{Slug: "fixture-converted", Start: "2030-05-01", Zones: fixtureZones}
	cases := map[string]func(o *convertOptions, in []byte) []byte{
		"a slug that is a path":      func(o *convertOptions, in []byte) []byte { o.Slug = "../x"; return in },
		"a start that is not a date": func(o *convertOptions, in []byte) []byte { o.Start = "May 1"; return in },
		"another schema": func(o *convertOptions, in []byte) []byte {
			return bytes.Replace(in, []byte(`"schema": "roadtrip-route/0.1"`), []byte(`"schema": "something-else/1"`), 1)
		},
		"a day with no segment": func(o *convertOptions, in []byte) []byte {
			return bytes.Replace(in, []byte(`"id": "d2", "day": 2`), []byte(`"id": "d2", "day": 5`), 1)
		},
		"a segment to nowhere": func(o *convertOptions, in []byte) []byte {
			return bytes.Replace(in, []byte(`"from": "cedar_camp", "to": "dogwood"`), []byte(`"from": "cedar_camp", "to": "atlantis"`), 1)
		},
	}
	for name, mutate := range cases {
		o := base
		in := mutate(&o, fixtureRoute(t))
		if _, err := convertRoute(in, o); err == nil {
			t.Errorf("%s: converted without complaint", name)
		}
	}
}

// With no -out the trip lands where the app reads trips from, and loads.
func TestRunConvertWritesIntoTheTripsDir(t *testing.T) {
	app := testApp(t)
	in := filepath.Join(t.TempDir(), "route.json")
	if err := os.WriteFile(in, fixtureRoute(t), 0o600); err != nil {
		t.Fatal(err)
	}
	msg, err := runConvert(in, "", app.dataDir, convertOptions{Slug: "fixture-converted", Start: "2030-05-01", Zones: fixtureZones})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	path := tripPath(app.dataDir, "fixture-converted")
	if !strings.Contains(msg, path) || !strings.Contains(msg, "1320.0 mi over 3 day(s), 1 long day(s)") {
		t.Errorf("summary %q", msg)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the trip file: %v, %v", fi, err)
	}
	if loaded, failed := loadVolumeTrips(app.store, app.dataDir); loaded != 1 || failed != 0 {
		t.Fatalf("the converted trip did not load at boot: %d loaded, %d failed", loaded, failed)
	}
	if c := rideCensus(t, app, "fixture-converted"); c.Legs != 3 || c.Checkpoints != 5 || c.StartDate != "2030-05-01" {
		t.Errorf("the converted ride: %+v", c)
	}
	// An explicit -out is honoured.
	out := filepath.Join(t.TempDir(), "elsewhere", "trip.geojson")
	if _, err := runConvert(in, out, app.dataDir, convertOptions{Slug: "fixture-converted", Start: "2030-05-01", Zones: fixtureZones}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("-out was not written: %v", err)
	}
}
