package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The fixture is synthetic by construction: invented places at invented
// coordinates, real IANA zones. Its two private places are the start and the
// finish, and the four numbers below are their RAW coordinates — the ones no
// page, pin or line may ever carry.
const fixtureSlug = "fixture-trip"

var fixtureRaw = []string{"40.1234", "100.4321", "44.3217", "106.2789"}

// What the store must hand out instead: the 0.05° cell of each.
var (
	fixtureStart  = LatLng{Lat: 40.1, Lng: -100.45}
	fixtureFinish = LatLng{Lat: 44.3, Lng: -106.3}
)

func fixtureTrip(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "trip-fixture.geojson"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

// send runs one request through the real mux: requireAuth, the visibility
// gate and the routing are all part of what these tests are about.
func send(t *testing.T, app *App, method, target, token, ctype string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, req)
	return w
}

func pushTrip(t *testing.T, app *App, body []byte, query string) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, app, "POST", "/admin/trip"+query, testToken, "application/geo+json", body)
}

func postForm(t *testing.T, app *App, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, app, "POST", target, testToken, "application/x-www-form-urlencoded", []byte(form.Encode()))
}

// activeTripApp is an app whose active ride is the fixture trip, pushed over
// HTTP the way the trip-keeper pushes one.
func activeTripApp(t *testing.T) *App {
	t.Helper()
	app := tokenApp(t, testToken)
	if w := pushTrip(t, app, fixtureTrip(t), "?activate=1"); w.Code != http.StatusOK {
		t.Fatalf("push: status %d: %s", w.Code, w.Body.String())
	}
	return app
}

func tripFiles(t *testing.T, app *App) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(filepath.Join(app.dataDir, tripsDir), func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func rideCensus(t *testing.T, app *App, slug string) RideCensus {
	t.Helper()
	rides, err := app.store.Rides()
	if err != nil {
		t.Fatalf("rides: %v", err)
	}
	for _, r := range rides {
		if r.Slug == slug {
			return r
		}
	}
	t.Fatalf("ride %s is not in the database", slug)
	return RideCensus{}
}

func mustLegs(t *testing.T, app *App, slug string) []Leg {
	t.Helper()
	legs, err := app.store.Legs(slug)
	if err != nil {
		t.Fatalf("legs: %v", err)
	}
	return legs
}

// --- A. trip files on the volume --------------------------------------------------

// Boot: a trip file on the volume becomes a ride through the same path a push
// takes, and a second boot refreshes it instead of doubling it.
func TestVolumeTripLoadsAtBoot(t *testing.T) {
	app := testApp(t)
	before := rideCensus(t, app, app.rideSlug)
	dir := filepath.Join(app.dataDir, tripsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fixtureSlug+".geojson"), fixtureTrip(t), 0o600); err != nil {
		t.Fatal(err)
	}
	for boot := 1; boot <= 2; boot++ {
		loaded, failed := loadVolumeTrips(app.store, app.dataDir)
		if loaded != 1 || failed != 0 {
			t.Fatalf("boot %d: loaded %d failed %d, want 1 and 0", boot, loaded, failed)
		}
		c := rideCensus(t, app, fixtureSlug)
		if c.Legs != 3 || c.Checkpoints != 5 {
			t.Fatalf("boot %d: %d legs, %d checkpoints; want 3 and 5", boot, c.Legs, c.Checkpoints)
		}
	}
	// The ride the image carries is not the volume's to touch.
	if after := rideCensus(t, app, app.rideSlug); after != before {
		t.Errorf("the embedded ride changed: %+v -> %+v", before, after)
	}
	// Loading a trip does not make it the ride the site shows.
	if got := app.activeSlug(); got != app.rideSlug {
		t.Errorf("active ride is %s after a boot load; want the fallback %s", got, app.rideSlug)
	}
}

// A file that will not load is skipped and counted, and never stops the boot
// or the files beside it.
func TestVolumeTripBadFileIsSkippedNotFatal(t *testing.T) {
	app := testApp(t)
	dir := filepath.Join(app.dataDir, tripsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, body []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(fixtureSlug+".geojson", fixtureTrip(t))
	write("broken.geojson", []byte("{ not json"))
	write("misnamed.geojson", fixtureTrip(t)) // names ride fixture-trip, not misnamed
	write(app.rideSlug+".geojson", fixtureTrip(t))
	loaded, failed := loadVolumeTrips(app.store, app.dataDir)
	if loaded != 1 || failed != 3 {
		t.Fatalf("loaded %d failed %d, want 1 and 3", loaded, failed)
	}
	if c := rideCensus(t, app, fixtureSlug); c.Legs != 3 {
		t.Errorf("the good file beside the bad ones did not load: %+v", c)
	}
}

func TestTripPushRequiresACredential(t *testing.T) {
	app := tokenApp(t, testToken)
	for name, token := range map[string]string{"no credential": "", "wrong token": "ffffffffffffffffffffffff"} {
		w := send(t, app, "POST", "/admin/trip?activate=1", token, "application/geo+json", fixtureTrip(t))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, w.Code)
		}
	}
	if files := tripFiles(t, app); len(files) != 0 {
		t.Errorf("a refused push wrote %v", files)
	}
	if _, err := app.store.Ride(fixtureSlug); err == nil {
		t.Error("a refused push created the ride")
	}
	if got := app.activeSlug(); got != app.rideSlug {
		t.Errorf("a refused push moved the active ride to %s", got)
	}
}

// The slug becomes a file name. One that is really a path is refused before
// anything is written anywhere.
func TestTripPushRefusesASlugThatIsAPath(t *testing.T) {
	app := tokenApp(t, testToken)
	for _, slug := range []string{"../../escape", "..", "a/b", "/etc/passwd", "UPPER", "trailing-", "a--b", ""} {
		body := bytes.Replace(fixtureTrip(t), []byte(`"slug": "fixture-trip"`), []byte(`"slug": `+strconv.Quote(slug)), 1)
		if w := pushTrip(t, app, body, ""); w.Code != http.StatusBadRequest {
			t.Errorf("slug %q: status %d, want 400: %s", slug, w.Code, w.Body.String())
		}
	}
	if files := tripFiles(t, app); len(files) != 0 {
		t.Errorf("a refused slug wrote %v", files)
	}
	// Nothing landed beside or above the data dir either.
	for _, p := range []string{filepath.Join(app.dataDir, "escape.geojson"),
		filepath.Join(filepath.Dir(app.dataDir), "escape.geojson"),
		filepath.Join(filepath.Dir(filepath.Dir(app.dataDir)), "escape.geojson")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("a traversal slug wrote %s", p)
		}
	}
}

// A document is parsed completely before a byte of it is written: bad JSON,
// and JSON that is not a loadable trip, both leave the volume and the
// database exactly as they were.
func TestTripPushMalformedWritesNothing(t *testing.T) {
	app := tokenApp(t, testToken)
	good := fixtureTrip(t)
	bad := map[string][]byte{
		"not json":                 []byte(`{"type": "FeatureCollection", `),
		"no legs":                  []byte(`{"ride": {"slug": "fixture-trip", "start_date": "2030-05-01"}, "features": []}`),
		"a checkpoint with no tz":  bytes.Replace(good, []byte(`"tz": "America/Denver", "precision": "approx", "is_terminal": true},`), []byte(`"precision": "approx", "is_terminal": true},`), 1),
		"a zone that is not IANA":  bytes.Replace(good, []byte(`"America/Chicago"`), []byte(`"Central Time"`), 1),
		"an undeclared choice":     bytes.Replace(good, []byte(`"choice": "valley"}`), []byte(`"choice": "tunnel"}`), 1),
		"a default that is absent": bytes.Replace(good, []byte(`"default": "ridge"`), []byte(`"default": "nowhere"`), 1),
		"a gap in the legs":        bytes.Replace(good, []byte(`"leg": 2, "day": 2, "slug": "day-2"`), []byte(`"leg": 7, "day": 2, "slug": "day-2"`), 1),
	}
	before, _ := app.store.Rides()
	for name, body := range bad {
		if bytes.Equal(body, good) {
			t.Fatalf("%s: the mutation did not change the fixture", name)
		}
		if w := pushTrip(t, app, body, "?activate=1"); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
	if files := tripFiles(t, app); len(files) != 0 {
		t.Errorf("a malformed push wrote %v", files)
	}
	after, _ := app.store.Rides()
	if len(after) != len(before) {
		t.Errorf("a malformed push changed the rides: %d -> %d", len(before), len(after))
	}
	if got := app.activeSlug(); got != app.rideSlug {
		t.Errorf("a malformed push moved the active ride to %s", got)
	}
}

// The whole path: pushed with the token, written to the volume byte for byte,
// in the database at once, and shown when activated — with the earlier ride
// still whole behind it.
func TestTripPushStoresAppliesAndActivates(t *testing.T) {
	app := tokenApp(t, testToken)
	earlier := rideCensus(t, app, app.rideSlug)
	body := fixtureTrip(t)

	w := pushTrip(t, app, body, "")
	if w.Code != http.StatusOK {
		t.Fatalf("push: status %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		Ride        string `json:"ride"`
		Created     bool   `json:"created"`
		Legs        int    `json:"legs"`
		Checkpoints int    `json:"checkpoints"`
		Active      bool   `json:"active"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("response: %v", err)
	}
	if res.Ride != fixtureSlug || !res.Created || res.Legs != 3 || res.Checkpoints != 5 || res.Active {
		t.Errorf("response %+v; want the fixture created with 3 legs, 5 checkpoints, not active", res)
	}
	path := tripPath(app.dataDir, fixtureSlug)
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the trip file is not on the volume: %v", err)
	}
	if !bytes.Equal(onDisk, body) {
		t.Error("the file on the volume is not the document that was pushed")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("trip file mode %v, want 0600", fi.Mode().Perm())
	}
	if files := tripFiles(t, app); len(files) != 1 {
		t.Errorf("the trips dir holds %v; want the one file and no leftover", files)
	}

	// Not active yet: the site still shows the earlier ride.
	home := send(t, app, "GET", "/", "", "", nil)
	if strings.Contains(home.Body.String(), "Alder to Elm") {
		t.Error("the pushed trip is on / before it was activated")
	}
	if w := postForm(t, app, "/admin/ride/active", url.Values{"slug": {fixtureSlug}}); w.Code != http.StatusOK {
		t.Fatalf("activate: status %d: %s", w.Code, w.Body.String())
	}
	home = send(t, app, "GET", "/", "", "", nil)
	if home.Code != http.StatusOK || !strings.Contains(home.Body.String(), "Alder to Elm") {
		t.Errorf("/ does not show the activated trip (status %d)", home.Code)
	}
	if after := rideCensus(t, app, app.rideSlug); after != earlier {
		t.Errorf("the earlier ride changed: %+v -> %+v", earlier, after)
	}

	// A second push of the same document is a refresh, not a second ride.
	w = pushTrip(t, app, body, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"created": false`) {
		t.Errorf("re-push: status %d: %s", w.Code, w.Body.String())
	}
	if c := rideCensus(t, app, fixtureSlug); c.Legs != 3 || c.Checkpoints != 5 {
		t.Errorf("after a re-push: %+v", c)
	}
}

// A push may not take the slug of a ride the image carries: boot applies the
// embedded seed first and the two would overwrite each other forever.
func TestTripPushRefusesAnEmbeddedRideSlug(t *testing.T) {
	app := tokenApp(t, testToken)
	before := rideCensus(t, app, app.rideSlug)
	body := bytes.Replace(fixtureTrip(t), []byte(`"slug": "fixture-trip"`), []byte(`"slug": `+strconv.Quote(app.rideSlug)), 1)
	if w := pushTrip(t, app, body, ""); w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", w.Code, w.Body.String())
	}
	if files := tripFiles(t, app); len(files) != 0 {
		t.Errorf("wrote %v", files)
	}
	if after := rideCensus(t, app, app.rideSlug); after != before {
		t.Errorf("the embedded ride changed: %+v -> %+v", before, after)
	}
}

// --- the active ride ------------------------------------------------------------

func TestActiveRideResolution(t *testing.T) {
	app := tokenApp(t, testToken)
	seedSlug := app.rideSlug
	if w := pushTrip(t, app, fixtureTrip(t), ""); w.Code != http.StatusOK {
		t.Fatalf("push: %d %s", w.Code, w.Body.String())
	}
	if got := app.activeSlug(); got != seedSlug {
		t.Fatalf("with nothing set the active ride is %s, want RIDE_SLUG %s", got, seedSlug)
	}
	// Setting it is an authenticated act.
	w := send(t, app, "POST", "/admin/ride/active", "", "application/x-www-form-urlencoded", []byte("slug="+fixtureSlug))
	if w.Code != http.StatusUnauthorized || app.activeSlug() != seedSlug {
		t.Fatalf("an anonymous activate: status %d, active %s", w.Code, app.activeSlug())
	}
	// A ride that is not in the database cannot be made active.
	if w := postForm(t, app, "/admin/ride/active", url.Values{"slug": {"no-such-ride"}}); w.Code != http.StatusNotFound {
		t.Errorf("unknown ride: status %d, want 404", w.Code)
	}
	if w := postForm(t, app, "/admin/ride/active", url.Values{"slug": {"../x"}}); w.Code != http.StatusBadRequest {
		t.Errorf("a slug that is a path: status %d, want 400", w.Code)
	}
	if app.activeSlug() != seedSlug {
		t.Fatalf("a refused activate moved the active ride to %s", app.activeSlug())
	}
	if w := postForm(t, app, "/admin/ride/active", url.Values{"slug": {fixtureSlug}}); w.Code != http.StatusOK {
		t.Fatalf("activate: status %d: %s", w.Code, w.Body.String())
	}
	if got := app.activeSlug(); got != fixtureSlug {
		t.Fatalf("active ride %s, want %s", got, fixtureSlug)
	}
	// Every surface moves together: the open data names the same ride.
	geo := send(t, app, "GET", "/route.geojson", "", "", nil)
	if !strings.Contains(geo.Body.String(), `"ride": "`+fixtureSlug+`"`) {
		t.Error("/route.geojson does not name the active ride")
	}
	// The census of rides says which is active and that both are whole.
	rides := send(t, app, "GET", "/admin/rides.json", testToken, "", nil)
	var census struct {
		Active string `json:"active"`
		Rides  []struct {
			Slug     string `json:"slug"`
			Legs     int    `json:"legs"`
			Active   bool   `json:"active"`
			TripFile bool   `json:"trip_file"`
		} `json:"rides"`
	}
	if err := json.Unmarshal(rides.Body.Bytes(), &census); err != nil {
		t.Fatalf("rides.json: %v: %s", err, rides.Body.String())
	}
	if census.Active != fixtureSlug || len(census.Rides) != 2 {
		t.Fatalf("rides.json %+v", census)
	}
	for _, r := range census.Rides {
		if r.Legs == 0 || r.Active != (r.Slug == fixtureSlug) || r.TripFile != (r.Slug == fixtureSlug) {
			t.Errorf("rides.json row %+v", r)
		}
	}
	if w := send(t, app, "GET", "/admin/rides.json", "", "", nil); w.Code == http.StatusOK {
		t.Error("/admin/rides.json answered without a credential")
	}
	// A key that names a ride which is gone falls back rather than 404ing the site.
	if err := app.store.SetMeta(metaActiveRide, "vanished"); err != nil {
		t.Fatal(err)
	}
	if got := app.activeSlug(); got != seedSlug {
		t.Errorf("a stale key resolved to %s, want the fallback", got)
	}
	_ = app.store.SetMeta(metaActiveRide, fixtureSlug)
	if w := postForm(t, app, "/admin/ride/active", url.Values{"clear": {"1"}}); w.Code != http.StatusOK || app.activeSlug() != seedSlug {
		t.Errorf("clear: status %d, active %s", w.Code, app.activeSlug())
	}
}

// --- B. options and the selection -------------------------------------------------

func parseFixture(t *testing.T, sel *Selection) *seedData {
	t.Helper()
	sd, err := parseSeedBytes(fixtureTrip(t), "", sel)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return sd
}

func legSlugs(sd *seedData) string {
	var s []string
	for _, l := range sd.Legs {
		s = append(s, l.Slug)
	}
	return strings.Join(s, " ")
}

func cpNames(sd *seedData) string {
	var s []string
	for _, c := range sd.Checkpoints {
		s = append(s, strconv.Itoa(c.Ord)+":"+c.Name+"@"+strconv.Itoa(c.LegOrd))
	}
	return strings.Join(s, " ")
}

func TestSelectionDefaults(t *testing.T) {
	sd := parseFixture(t, nil)
	if got := legSlugs(sd); got != "day-1-ridge day-2 day-3-direct" {
		t.Errorf("legs %q", got)
	}
	if got := cpNames(sd); got != "1:Alder@1 2:Birch Gate@1 3:Cedar Camp@1 4:Dogwood@2 5:Elm@3" {
		t.Errorf("checkpoints %q", got)
	}
	// A leg with a day and no date is dated from the start date.
	for i, want := range []string{"2030-05-01", "2030-05-02", "2030-05-03"} {
		if sd.Legs[i].Date != want {
			t.Errorf("leg %d dated %s, want %s", i+1, sd.Legs[i].Date, want)
		}
	}
	if sd.Ride.StartDate != "2030-05-01" || sd.Ride.EndDate != "2030-05-03" {
		t.Errorf("ride spans %s..%s", sd.Ride.StartDate, sd.Ride.EndDate)
	}
	// est_hours = miles / avg_mph.
	if sd.Legs[0].EstHours != 10 || sd.Legs[1].EstHours != 5 {
		t.Errorf("hours %v %v, want 10 and 5", sd.Legs[0].EstHours, sd.Legs[1].EstHours)
	}
	// The parse reports the selection it took, fully resolved.
	if sd.Selection.Choices["day1"] != "ridge" || sd.Selection.Choices["finish"] != "direct" || sd.Selection.StartDate != "2030-05-01" {
		t.Errorf("resolved selection %+v", sd.Selection)
	}
	if sd.Ride.OriginLabel != "Alder" || sd.Ride.TerminusLabel != "Elm" {
		t.Errorf("ride runs %s -> %s", sd.Ride.OriginLabel, sd.Ride.TerminusLabel)
	}
}

func TestSelectionOverride(t *testing.T) {
	sd := parseFixture(t, &Selection{Choices: map[string]string{"day1": "valley"}})
	if got := legSlugs(sd); got != "day-1-valley day-2 day-3-direct" {
		t.Errorf("legs %q", got)
	}
	if sd.Legs[0].EstMiles != 540 || sd.Legs[0].EstHours != 9 {
		t.Errorf("leg 1 is %v mi, %v h; want 540 and 9", sd.Legs[0].EstMiles, sd.Legs[0].EstHours)
	}
	// The option it did not answer keeps its default.
	if sd.Selection.Choices["finish"] != "direct" {
		t.Errorf("finish resolved to %q", sd.Selection.Choices["finish"])
	}
}

// One choice can add a whole day and a checkpoint, and renumber the finish.
func TestSelectionChoiceAddsADayAndACheckpoint(t *testing.T) {
	sd := parseFixture(t, &Selection{Choices: map[string]string{"finish": "stopover"}})
	if got := legSlugs(sd); got != "day-1-ridge day-2 day-3-stopover day-4-stopover" {
		t.Errorf("legs %q", got)
	}
	if got := cpNames(sd); got != "1:Alder@1 2:Birch Gate@1 3:Cedar Camp@1 4:Dogwood@2 5:Fir Hollow@3 6:Elm@4" {
		t.Errorf("checkpoints %q", got)
	}
	if sd.Ride.EndDate != "2030-05-04" {
		t.Errorf("the ride ends %s; a choice that adds a day must move the end to 2030-05-04", sd.Ride.EndDate)
	}
	if sd.Ride.TerminusLabel != "Elm" {
		t.Errorf("terminus %q", sd.Ride.TerminusLabel)
	}
}

// The start date is one field: moving it re-dates every leg.
func TestSelectionStartDateMovesEveryLeg(t *testing.T) {
	sd := parseFixture(t, &Selection{StartDate: "2030-05-02"})
	for i, want := range []string{"2030-05-02", "2030-05-03", "2030-05-04"} {
		if sd.Legs[i].Date != want {
			t.Errorf("leg %d dated %s, want %s", i+1, sd.Legs[i].Date, want)
		}
	}
	if sd.Ride.StartDate != "2030-05-02" || sd.Ride.EndDate != "2030-05-04" {
		t.Errorf("ride spans %s..%s", sd.Ride.StartDate, sd.Ride.EndDate)
	}
	if _, err := parseSeedBytes(fixtureTrip(t), "", &Selection{StartDate: "next week"}); err == nil {
		t.Error("a start date that is not a date was accepted")
	}
}

// A stored selection outlives the document it was made against: a choice the
// document no longer has falls back to that option's default.
func TestSelectionUnknownChoiceTakesTheDefault(t *testing.T) {
	sd := parseFixture(t, &Selection{Choices: map[string]string{"day1": "tunnel", "nonsense": "x"}})
	if got := legSlugs(sd); got != "day-1-ridge day-2 day-3-direct" {
		t.Errorf("legs %q", got)
	}
	if len(sd.Selection.Choices) != 2 {
		t.Errorf("resolved selection carries %v; want exactly the document's two options", sd.Selection.Choices)
	}
}

// The embedded seed has no options and must parse exactly as it always has.
func TestSeedWithoutOptionsIsUnchanged(t *testing.T) {
	app := testApp(t)
	raw, err := os.ReadFile(stageSeed(t, app))
	if err != nil {
		t.Fatal(err)
	}
	sd, err := parseSeedBytes(raw, "x", nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(sd.Options) != 0 || len(sd.Selection.Choices) != 0 {
		t.Errorf("the seed grew options: %+v", sd.Selection)
	}
	if len(sd.Legs) != 4 || len(sd.Checkpoints) != 6 {
		t.Errorf("%d legs, %d checkpoints; want 4 and 6", len(sd.Legs), len(sd.Checkpoints))
	}
	if sd.Legs[0].EstHours != 10 || sd.Legs[0].Date != "2026-09-04" {
		t.Errorf("leg 1: %v h on %s; the seed's own figures must stand", sd.Legs[0].EstHours, sd.Legs[0].Date)
	}
}

// --- totals and the long-day flag ---------------------------------------------------

func fixturePlan(t *testing.T) *planDoc {
	t.Helper()
	doc, err := publishPlan(fixtureTrip(t))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return doc
}

func TestPlanTotalsAndLongDay(t *testing.T) {
	doc := fixturePlan(t)
	cases := []struct {
		name     string
		choices  map[string]string
		miles    float64
		shown    string
		days     int
		day1     float64
		day1h    float64
		day1Long bool
		longDays int
	}{
		{"defaults", nil, 1320, "1,320", 3, 600, 10, true, 1},
		{"the shorter day 1", map[string]string{"day1": "valley"}, 1260, "1,260", 3, 540, 9, false, 0},
		{"the stopover", map[string]string{"finish": "stopover"}, 1280, "1,280", 4, 600, 10, true, 1},
		{"both", map[string]string{"day1": "valley", "finish": "stopover"}, 1220, "1,220", 4, 540, 9, false, 0},
	}
	for _, c := range cases {
		s := doc.summarize(doc.choices(c.choices), doc.StartDate)
		if s.TotalMiles != c.miles || s.TotalMilesShown != c.shown || s.DayCount != c.days || s.LongDays != c.longDays {
			t.Errorf("%s: %v mi (%s) over %d days, %d long; want %v (%s), %d, %d",
				c.name, s.TotalMiles, s.TotalMilesShown, s.DayCount, s.LongDays, c.miles, c.shown, c.days, c.longDays)
			continue
		}
		d := s.Days[0]
		if d.Miles != c.day1 || d.Hours != c.day1h || d.Long != c.day1Long || d.Date != "2030-05-01" {
			t.Errorf("%s: day 1 is %+v", c.name, d)
		}
	}
	// The stopover's last day exists only under that choice, and is dated.
	s := doc.summarize(doc.choices(map[string]string{"finish": "stopover"}), doc.StartDate)
	if last := s.Days[3]; last.Day != 4 || last.Miles != 180 || last.Date != "2030-05-04" || s.EndDate != "2030-05-04" {
		t.Errorf("day 4 is %+v, plan ends %s", last, s.EndDate)
	}
	if len(s.Checkpoints) != 6 || s.Checkpoints[4].Name != "Fir Hollow" || s.Checkpoints[4].Label != "Night 3" {
		t.Errorf("stopover checkpoints: %+v", s.Checkpoints)
	}
}

// The flag is "over the mark", judged on the hours as driven, not as shown.
func TestLongDayIsStrictlyOverTheMark(t *testing.T) {
	day := func(miles float64) planDay {
		doc := &planDoc{AvgMPH: 60, LongDayHours: 9.5, Legs: []planLeg{{Ord: 1, Day: 1, Miles: miles}}}
		return doc.summarize(nil, "2030-05-01").Days[0]
	}
	if d := day(570); d.Long || d.Hours != 9.5 {
		t.Errorf("570 mi at 60 mph is exactly 9.5 h and must not be flagged: %+v", d)
	}
	if d := day(571.2); !d.Long {
		t.Errorf("571.2 mi at 60 mph is 9.52 h, shown as 9.5, and must be flagged: %+v", d)
	}
	// No mark, no flag.
	doc := &planDoc{AvgMPH: 60, Legs: []planLeg{{Ord: 1, Day: 1, Miles: 900}}}
	if doc.summarize(nil, "2030-05-01").Days[0].Long {
		t.Error("a document with no long_day_hours flagged a day")
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1986: "1,986", 1234567: "1,234,567"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}

// --- D. the planner page ---------------------------------------------------------------

type planJSONBody struct {
	Plan  planSummary `json:"plan"`
	Saved struct {
		Selection map[string]string `json:"selection"`
		StartDate string            `json:"start_date"`
	} `json:"saved"`
}

func getPlanJSON(t *testing.T, app *App, query string) planJSONBody {
	t.Helper()
	w := send(t, app, "GET", "/plan.json"+query, "", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/plan.json%s: status %d: %s", query, w.Code, w.Body.String())
	}
	var b planJSONBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("/plan.json%s: %v", query, err)
	}
	return b
}

func TestPlanJSONAnswersForAnySelectionWithoutSavingIt(t *testing.T) {
	app := activeTripApp(t)
	if b := getPlanJSON(t, app, ""); b.Plan.TotalMiles != 1320 || b.Plan.DayCount != 3 || !b.Plan.Days[0].Long {
		t.Errorf("default plan: %+v", b.Plan)
	}
	if b := getPlanJSON(t, app, "?opt-day1=valley"); b.Plan.TotalMiles != 1260 || b.Plan.Days[0].Long {
		t.Errorf("valley: %+v", b.Plan)
	}
	b := getPlanJSON(t, app, "?opt-finish=stopover&start=2030-06-10")
	if b.Plan.TotalMiles != 1280 || b.Plan.DayCount != 4 || b.Plan.Days[3].Date != "2030-06-13" {
		t.Errorf("stopover from a later start: %+v", b.Plan)
	}
	// Asking is not saving.
	if b.Saved.Selection["finish"] != "direct" || b.Saved.StartDate != "2030-05-01" {
		t.Errorf("a GET changed what is saved: %+v", b.Saved)
	}
	if c := rideCensus(t, app, fixtureSlug); c.Legs != 3 {
		t.Errorf("a GET re-planned the ride: %+v", c)
	}
	for _, q := range []string{"?opt-day1=tunnel", "?opt-nonsense=x", "?start=tomorrow"} {
		if w := send(t, app, "GET", "/plan.json"+q, "", "", nil); w.Code != http.StatusBadRequest {
			t.Errorf("/plan.json%s: status %d, want 400", q, w.Code)
		}
	}
}

func TestPlanPageRenders(t *testing.T) {
	app := activeTripApp(t)
	w := send(t, app, "GET", "/plan", "", "", nil)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, body)
	}
	for _, want := range []string{"1,320 mi", "3 days", "Long day", "Day 1 route", "Ridge road", "Via a stopover",
		`name="opt-day1" value="ridge" checked`, `name="opt-finish" value="direct" checked`,
		`name="start" value="2030-05-01"`, "window.PLAN", "/static/js/plan.js", "Wed May 1"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	// A visitor gets no save control; the trip-keeper does.
	if strings.Contains(body, `formaction="/admin/plan"`) {
		t.Error("an anonymous visitor was offered Save")
	}
	if w := send(t, app, "GET", "/plan", testToken, "", nil); !strings.Contains(w.Body.String(), `formaction="/admin/plan"`) {
		t.Error("the signed-in trip-keeper was not offered Save")
	}
	// The same page, asked for another selection, is rendered for it.
	alt := send(t, app, "GET", "/plan?opt-day1=valley&opt-finish=stopover", "", "", nil).Body.String()
	for _, want := range []string{"1,220 mi", "4 days", `name="opt-day1" value="valley" checked`} {
		if !strings.Contains(alt, want) {
			t.Errorf("the alternative page lacks %q", want)
		}
	}
	if strings.Contains(alt, "Long day") {
		t.Error("the shorter day 1 is still flagged")
	}
	// No dark-mode rule and no modal in what this page adds.
	if strings.Contains(body, "<dialog") || strings.Contains(body, "confirm(") {
		t.Error("the planner page opens a dialog")
	}
}

// A ride with no trip file — the embedded seed — still has a plan page: its
// days, with nothing to choose.
func TestPlanPageForARideWithNoTripFile(t *testing.T) {
	app := tokenApp(t, testToken)
	w := send(t, app, "GET", "/plan", testToken, "", nil)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(body, "4 days") || strings.Contains(body, `type="radio"`) || strings.Contains(body, `formaction="/admin/plan"`) {
		t.Error("the seed ride's plan page should list 4 days and offer nothing to choose or save")
	}
	if w := postForm(t, app, "/admin/plan", url.Values{"start": {"2030-01-01"}}); w.Code != http.StatusConflict {
		t.Errorf("saving a selection for a ride with no trip file: status %d, want 409", w.Code)
	}
}

// --- the fuzz seam -------------------------------------------------------------------

// near reports whether a point is inside a private place's mask without being
// the snapped point itself — a coordinate that got past the seam.
func leaked(lat, lng float64) bool {
	for _, c := range []LatLng{fixtureStart, fixtureFinish} {
		if lat == c.Lat && lng == c.Lng {
			return false
		}
		if haversineKm(lat, lng, c.Lat, c.Lng) < lineMaskKm {
			return true
		}
	}
	return false
}

var planScriptRE = regexp.MustCompile(`window\.PLAN = (.*);</script>`)

// Every coordinate the planner is given — the taken road, the roads not
// taken, every checkpoint of every choice — has been through the seam.
func TestPlanDataNeverCarriesAPrivateCoordinate(t *testing.T) {
	app := activeTripApp(t)
	page := send(t, app, "GET", "/plan?opt-day1=valley&opt-finish=stopover", "", "", nil).Body.String()
	m := planScriptRE.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no window.PLAN on the page")
	}
	var plan struct {
		Doc planDoc `json:"doc"`
	}
	if err := json.Unmarshal([]byte(m[1]), &plan); err != nil {
		t.Fatalf("window.PLAN: %v", err)
	}
	if len(plan.Doc.Legs) != 6 || len(plan.Doc.Checkpoints) != 7 {
		t.Fatalf("window.PLAN carries %d legs and %d checkpoints; want every alternative: 6 and 7",
			len(plan.Doc.Legs), len(plan.Doc.Checkpoints))
	}
	vertices := 0
	for _, l := range plan.Doc.Legs {
		var pts [][]float64
		if err := json.Unmarshal(l.Line, &pts); err != nil || len(pts) < 2 {
			t.Fatalf("leg %s has no line", l.Slug)
		}
		for _, p := range pts {
			vertices++
			if leaked(p[1], p[0]) {
				t.Errorf("leg %s (%s/%s) draws [%v, %v], inside a private place's mask", l.Slug, l.Option, l.Choice, p[0], p[1])
			}
		}
	}
	// The lines still reach the pins: each road out of the start begins AT the
	// snapped start, each road into the finish ends at the snapped finish.
	ends := map[string][2]LatLng{}
	for _, l := range plan.Doc.Legs {
		var pts [][]float64
		_ = json.Unmarshal(l.Line, &pts)
		ends[l.Slug] = [2]LatLng{{Lat: pts[0][1], Lng: pts[0][0]}, {Lat: pts[len(pts)-1][1], Lng: pts[len(pts)-1][0]}}
	}
	for _, slug := range []string{"day-1-ridge", "day-1-valley"} {
		if ends[slug][0] != fixtureStart {
			t.Errorf("%s starts at %+v, want the snapped start %+v", slug, ends[slug][0], fixtureStart)
		}
	}
	for _, slug := range []string{"day-3-direct", "day-4-stopover"} {
		if ends[slug][1] != fixtureFinish {
			t.Errorf("%s ends at %+v, want the snapped finish %+v", slug, ends[slug][1], fixtureFinish)
		}
	}
	fuzzed := 0
	for _, c := range plan.Doc.Checkpoints {
		if leaked(c.Lat, c.Lng) {
			t.Errorf("checkpoint %d %s (%s/%s) is at [%v, %v], a raw private point", c.Ord, c.Name, c.Option, c.Choice, c.Lng, c.Lat)
		}
		if c.Precision == precisionFuzzed {
			fuzzed++
		}
	}
	// The check above is only worth something if it was fed the private places.
	if vertices < 12 || fuzzed != 3 {
		t.Fatalf("the check saw %d vertices and %d fuzzed checkpoints; the fixture should give it more than that", vertices, fuzzed)
	}
}

// The raw numbers themselves appear on no surface, for any selection.
func TestNoSurfaceCarriesTheRawPrivateNumbers(t *testing.T) {
	app := activeTripApp(t)
	// Positive control: the document on the volume really does hold them.
	onDisk, _ := os.ReadFile(tripPath(app.dataDir, fixtureSlug))
	for _, raw := range fixtureRaw {
		if !bytes.Contains(onDisk, []byte(raw)) {
			t.Fatalf("the fixture on the volume does not contain %s; this test would pass on nothing", raw)
		}
	}
	paths := []string{"/", "/plan", "/plan?opt-day1=valley&opt-finish=stopover", "/plan.json",
		"/plan.json?opt-day1=valley", "/plan.json?opt-finish=stopover", "/route.geojson",
		"/legs/day-1-ridge", "/legs/day-3-direct", "/photos"}
	for _, token := range []string{"", testToken} {
		for _, p := range paths {
			w := send(t, app, "GET", p, token, "", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("%s: status %d", p, w.Code)
			}
			for _, raw := range fixtureRaw {
				if strings.Contains(w.Body.String(), raw) {
					t.Errorf("%s (signed in: %v) carries the raw private number %s", p, token != "", raw)
				}
			}
		}
	}
	// The signed-in screens that list checkpoints get the same published points.
	for _, p := range []string{"/admin/post", "/admin/lodging"} {
		w := send(t, app, "GET", p, testToken, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", p, w.Code)
		}
		for _, raw := range fixtureRaw {
			if strings.Contains(w.Body.String(), raw) {
				t.Errorf("%s carries the raw private number %s", p, raw)
			}
		}
	}
	// And after the plan is re-saved under the other finish.
	if w := postForm(t, app, "/admin/plan", url.Values{"opt-finish": {"stopover"}}); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	for _, p := range []string{"/", "/route.geojson", "/legs/day-4-stopover", "/plan"} {
		body := send(t, app, "GET", p, "", "", nil).Body.String()
		for _, raw := range fixtureRaw {
			if strings.Contains(body, raw) {
				t.Errorf("%s carries %s after the finish changed", p, raw)
			}
		}
	}
}

// The store's own read path: a leg line no longer hands back the point the
// checkpoint fuzz removed. This is the ride pages and the open data.
func TestStoreLegLinesStopAtThePrivatePlace(t *testing.T) {
	app := activeTripApp(t)
	legs := mustLegs(t, app, fixtureSlug)
	first, _ := legVertices(t, legs[0])
	if first[0] != fixtureStart.Lng || first[1] != fixtureStart.Lat {
		t.Errorf("leg 1 starts at %v, want the snapped start", first)
	}
	_, last := legVertices(t, legs[len(legs)-1])
	if last[0] != fixtureFinish.Lng || last[1] != fixtureFinish.Lat {
		t.Errorf("the last leg ends at %v, want the snapped finish", last)
	}
	for _, l := range legs {
		var pts [][]float64
		_ = json.Unmarshal(l.Line, &pts)
		for _, p := range pts {
			if leaked(p[1], p[0]) {
				t.Errorf("leg %d draws [%v, %v], inside a private place's mask", l.Ord, p[0], p[1])
			}
		}
	}
	// The embedded ride gets the same treatment: every leg that begins or ends
	// at a fuzzed checkpoint begins or ends AT its snapped point.
	seed := mustLegs(t, app, app.rideSlug)
	checked := 0
	for i := range seed {
		first, last := legVertices(t, seed[i])
		if c := legOrigin(seed, i); c != nil && c.Precision == precisionFuzzed {
			checked++
			if first[0] != c.Lng || first[1] != c.Lat {
				t.Errorf("seed leg %d starts at %v, not at fuzzed checkpoint %d [%v, %v]", seed[i].Ord, first, c.Ord, c.Lng, c.Lat)
			}
		}
		if c := legTerminus(seed[i]); c != nil && c.Precision == precisionFuzzed {
			checked++
			if last[0] != c.Lng || last[1] != c.Lat {
				t.Errorf("seed leg %d ends at %v, not at fuzzed checkpoint %d [%v, %v]", seed[i].Ord, last, c.Ord, c.Lng, c.Lat)
			}
		}
	}
	if checked < 3 {
		t.Fatalf("only %d seed leg ends meet a fuzzed checkpoint; the seed should have at least 3", checked)
	}
}

func TestMaskLine(t *testing.T) {
	home := Checkpoint{Lat: 40.1, Lng: -100.45, Precision: precisionFuzzed}
	line := func(pts string) json.RawMessage { return json.RawMessage(pts) }
	cases := []struct{ name, in, want string }{
		{"a run at the start collapses to the cell point",
			`[[-100.4321,40.1234],[-100.44,40.13],[-100.6,40.5],[-101,41]]`, `[[-100.45,40.1],[-100.6,40.5],[-101,41]]`},
		{"a run at the end",
			`[[-101,41],[-100.6,40.5],[-100.44,40.13],[-100.4321,40.1234]]`, `[[-101,41],[-100.6,40.5],[-100.45,40.1]]`},
		{"a pass through the middle leaves no gap",
			`[[-101,41],[-100.44,40.13],[-100.43,40.12],[-99.9,39.6]]`, `[[-101,41],[-100.45,40.1],[-99.9,39.6]]`},
		{"a line wholly inside stays a line",
			`[[-100.44,40.13],[-100.43,40.12]]`, `[[-100.45,40.1],[-100.45,40.1]]`},
		{"a line nowhere near is untouched",
			`[[-101,41],[-102,42]]`, `[[-101,41],[-102,42]]`},
		{"a line that cannot be read comes back empty, not raw",
			`{"not":"a line"}`, `[]`},
	}
	for _, c := range cases {
		if got := string(maskLine(line(c.in), []Checkpoint{home})); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
	// With no private place there is nothing to do, and nothing is done.
	in := `[[-100.4321,40.1234],[-101,41]]`
	if got := string(maskLine(line(in), nil)); got != in {
		t.Errorf("no private places: got %s", got)
	}
}

// --- C. the selection is guarded --------------------------------------------------------

func savedSelection(t *testing.T, app *App) Selection {
	t.Helper()
	sel := app.store.StoredSelection(fixtureSlug)
	if sel == nil {
		t.Fatal("no stored selection")
	}
	return *sel
}

func cpByOrd(t *testing.T, app *App, ord int) Checkpoint {
	t.Helper()
	for _, l := range mustLegs(t, app, fixtureSlug) {
		for _, c := range l.Checkpoints {
			if c.Ord == ord {
				return c
			}
		}
	}
	t.Fatalf("no checkpoint %d", ord)
	return Checkpoint{}
}

func TestSelectionSaveNeedsACredential(t *testing.T) {
	app := activeTripApp(t)
	w := send(t, app, "POST", "/admin/plan", "", "application/x-www-form-urlencoded", []byte("opt-finish=stopover"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", w.Code)
	}
	if c := rideCensus(t, app, fixtureSlug); c.Legs != 3 {
		t.Errorf("an anonymous save re-planned the ride: %+v", c)
	}
}

// A saved choice re-plans the ride at once: the day and the checkpoint the
// choice adds appear, and going back takes them away again.
func TestSelectionSaveAddsAndRemovesTheExtraDay(t *testing.T) {
	app := activeTripApp(t)
	if w := postForm(t, app, "/admin/plan", url.Values{"opt-finish": {"stopover"}}); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if c := rideCensus(t, app, fixtureSlug); c.Legs != 4 || c.Checkpoints != 6 || c.EndDate != "2030-05-04" {
		t.Fatalf("after choosing the stopover: %+v", c)
	}
	if c := cpByOrd(t, app, 5); c.Name != "Fir Hollow" || c.Precision != precisionApprox {
		t.Errorf("checkpoint 5 is %+v", c)
	}
	// The finish moved to 6 and is still a private place: snapped on the way out.
	if c := cpByOrd(t, app, 6); c.Name != "Elm" || c.Precision != precisionFuzzed || c.Lat != fixtureFinish.Lat || c.Lng != fixtureFinish.Lng {
		t.Errorf("checkpoint 6 is %+v", c)
	}
	if sel := savedSelection(t, app); sel.Choices["finish"] != "stopover" || sel.Choices["day1"] != "ridge" || sel.StartDate != "2030-05-01" {
		t.Errorf("stored selection %+v", sel)
	}
	// The saved selection survives a boot: the volume pass re-applies it.
	if loaded, failed := loadVolumeTrips(app.store, app.dataDir); loaded != 1 || failed != 0 {
		t.Fatalf("boot pass: %d loaded, %d failed", loaded, failed)
	}
	if c := rideCensus(t, app, fixtureSlug); c.Legs != 4 || c.Checkpoints != 6 {
		t.Fatalf("a boot dropped the saved selection: %+v", c)
	}
	// And back.
	if w := postForm(t, app, "/admin/plan", url.Values{"opt-finish": {"direct"}, "opt-day1": {"valley"}}); w.Code != http.StatusOK {
		t.Fatalf("save back: %d %s", w.Code, w.Body.String())
	}
	if c := rideCensus(t, app, fixtureSlug); c.Legs != 3 || c.Checkpoints != 5 || c.EndDate != "2030-05-03" {
		t.Fatalf("after going back to direct: %+v", c)
	}
	legs := mustLegs(t, app, fixtureSlug)
	if legs[0].Slug != "day-1-valley" || legs[0].EstMiles != 540 || legs[2].Slug != "day-3-direct" {
		t.Errorf("legs after the change: %s, %s", legs[0].Slug, legs[2].Slug)
	}
	if c := cpByOrd(t, app, 5); c.Name != "Elm" || c.Precision != precisionFuzzed {
		t.Errorf("checkpoint 5 is %+v", c)
	}
	// A choice the document does not have is refused, not defaulted.
	if w := postForm(t, app, "/admin/plan", url.Values{"opt-day1": {"tunnel"}}); w.Code != http.StatusBadRequest {
		t.Errorf("an unknown choice: status %d, want 400", w.Code)
	}
}

// Once the checkpoint a choice would replace is arrived, the choice is spent.
func TestSelectionGuardRefusesOnceTheCheckpointIsArrived(t *testing.T) {
	app := activeTripApp(t)
	finish := cpByOrd(t, app, 5) // Elm under "direct"; "stopover" would put Fir Hollow at 5
	at := time.Date(2030, 5, 3, 22, 0, 0, 0, time.UTC)
	if err := app.store.SetArrived(finish.ID, at, nil); err != nil {
		t.Fatal(err)
	}
	w := postForm(t, app, "/admin/plan", url.Values{"opt-finish": {"stopover"}})
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "checkpoint 5 is arrived") {
		t.Errorf("the refusal does not say why: %s", w.Body.String())
	}
	// Nothing moved.
	if c := rideCensus(t, app, fixtureSlug); c.Legs != 3 || c.Checkpoints != 5 || c.Arrived != 1 {
		t.Errorf("a refused change re-planned the ride: %+v", c)
	}
	got := cpByOrd(t, app, 5)
	if got.ID != finish.ID || got.Name != "Elm" || got.ArrivedAt != utcString(at) {
		t.Errorf("the arrived checkpoint was touched: %+v", got)
	}
	if sel := savedSelection(t, app); sel.Choices["finish"] != "direct" {
		t.Errorf("a refused change was stored: %+v", sel)
	}
	// A browser gets the same refusal as a message on the page, not a status.
	req := httptest.NewRequest("POST", "/admin/plan", strings.NewReader("opt-finish=stopover"))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rec := httptest.NewRecorder()
	app.routes().ServeHTTP(rec, req)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/plan?") || !strings.Contains(loc, "error=") || !strings.Contains(loc, "opt-finish=stopover") {
		t.Errorf("browser refusal: status %d, Location %q", rec.Code, loc)
	}
	// A choice that does not reach the arrived checkpoint is still open.
	if w := postForm(t, app, "/admin/plan", url.Values{"opt-day1": {"valley"}}); w.Code != http.StatusOK {
		t.Errorf("an unrelated choice was refused: %d %s", w.Code, w.Body.String())
	}
	if got := cpByOrd(t, app, 5); got.ArrivedAt != utcString(at) {
		t.Errorf("an unrelated choice cleared the arrival: %+v", got)
	}
}

// Once a leg holds a post, the road for that leg is spent — and a post is
// never deleted by a plan change.
func TestSelectionGuardRefusesOnceTheLegHoldsAPost(t *testing.T) {
	app := activeTripApp(t)
	legs := mustLegs(t, app, fixtureSlug)
	post := mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "note", TZ: "America/Chicago", Title: "on the ridge"})

	w := postForm(t, app, "/admin/plan", url.Values{"opt-day1": {"valley"}})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "leg 1 holds 1 post") {
		t.Fatalf("status %d, want 409 naming leg 1: %s", w.Code, w.Body.String())
	}
	if got := mustLegs(t, app, fixtureSlug)[0]; got.Slug != "day-1-ridge" || got.ID != legs[0].ID {
		t.Errorf("a refused change re-routed leg 1: %s", got.Slug)
	}
	if got := mustGet(t, app, post.ID); got.LegID != legs[0].ID || got.Title != "on the ridge" {
		t.Errorf("the post was touched: %+v", got)
	}
	// The other option does not reach leg 1 and is still open — and the post
	// rides through that change untouched.
	if w := postForm(t, app, "/admin/plan", url.Values{"opt-finish": {"stopover"}}); w.Code != http.StatusOK {
		t.Fatalf("an unrelated choice was refused: %d %s", w.Code, w.Body.String())
	}
	if c := rideCensus(t, app, fixtureSlug); c.Legs != 4 || c.Posts != 1 {
		t.Errorf("after the unrelated change: %+v", c)
	}
	// Now a post on the day the stopover added: the stopover cannot be undone,
	// because undoing it would drop that leg and the post with it.
	legs = mustLegs(t, app, fixtureSlug)
	last := mustCreate(t, app, &Post{LegID: legs[3].ID, Kind: "note", TZ: "America/Denver", Title: "the last stretch"})
	w = postForm(t, app, "/admin/plan", url.Values{"opt-finish": {"direct"}})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "leg 4 holds 1 post") {
		t.Fatalf("status %d, want 409 naming leg 4: %s", w.Code, w.Body.String())
	}
	if got := mustGet(t, app, last.ID); got.LegID != legs[3].ID {
		t.Errorf("the post on the added day was touched: %+v", got)
	}
	if c := rideCensus(t, app, fixtureSlug); c.Legs != 4 || c.Posts != 2 {
		t.Errorf("a refused change dropped something: %+v", c)
	}
}

// A post filed under a checkpoint pins that checkpoint too.
func TestSelectionGuardRefusesOnceTheCheckpointHoldsAPost(t *testing.T) {
	app := activeTripApp(t)
	finish := cpByOrd(t, app, 5)
	// Filed on leg 2 so that only the checkpoint, not the leg, is in the way.
	legs := mustLegs(t, app, fixtureSlug)
	post := mustCreate(t, app, &Post{LegID: legs[1].ID, CheckpointID: finish.ID, Kind: "note", TZ: "America/Denver", Title: "nearly there"})
	w := postForm(t, app, "/admin/plan", url.Values{"opt-finish": {"stopover"}})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "checkpoint 5 holds 1 post") {
		t.Fatalf("status %d, want 409 naming checkpoint 5: %s", w.Code, w.Body.String())
	}
	if got := mustGet(t, app, post.ID); got.CheckpointID != finish.ID || got.CheckpointName != "Elm" {
		t.Errorf("the post lost its checkpoint: %+v", got)
	}
}

// The departure slipping a day is the reason the start date is one field. It
// is never a conflict: it moves dates and nothing else.
func TestStartDateMovesWithPostsAndArrivalsInPlace(t *testing.T) {
	app := activeTripApp(t)
	legs := mustLegs(t, app, fixtureSlug)
	post := mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "note", TZ: "America/Chicago", Title: "packing"})
	start := cpByOrd(t, app, 1)
	at := time.Date(2030, 5, 2, 12, 0, 0, 0, time.UTC)
	if err := app.store.SetArrived(start.ID, at, nil); err != nil {
		t.Fatal(err)
	}
	if w := postForm(t, app, "/admin/plan", url.Values{"start": {"2030-05-02"}}); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	legs = mustLegs(t, app, fixtureSlug)
	for i, want := range []string{"2030-05-02", "2030-05-03", "2030-05-04"} {
		if legs[i].Date != want {
			t.Errorf("leg %d dated %s, want %s", i+1, legs[i].Date, want)
		}
	}
	ride, _ := app.store.Ride(fixtureSlug)
	if ride.StartDate != "2030-05-02" || ride.EndDate != "2030-05-04" {
		t.Errorf("ride spans %s..%s", ride.StartDate, ride.EndDate)
	}
	if got := mustGet(t, app, post.ID); got.LegID != legs[0].ID || got.Title != "packing" {
		t.Errorf("the post was touched: %+v", got)
	}
	if got := cpByOrd(t, app, 1); got.ArrivedAt != utcString(at) {
		t.Errorf("the arrival was touched: %+v", got)
	}
	if sel := savedSelection(t, app); sel.StartDate != "2030-05-02" {
		t.Errorf("stored start date %q", sel.StartDate)
	}
	if w := postForm(t, app, "/admin/plan", url.Values{"start": {"the 2nd"}}); w.Code != http.StatusBadRequest {
		t.Errorf("a start that is not a date: status %d, want 400", w.Code)
	}
}

// A re-pushed document is held to the same guard as a saved selection.
func TestTripRePushIsGuardedToo(t *testing.T) {
	app := activeTripApp(t)
	legs := mustLegs(t, app, fixtureSlug)
	post := mustCreate(t, app, &Post{LegID: legs[1].ID, Kind: "note", TZ: "America/Chicago", Title: "day two"})
	original, _ := os.ReadFile(tripPath(app.dataDir, fixtureSlug))
	// The new document gives day 2 a different road.
	changed := bytes.Replace(fixtureTrip(t), []byte(`"slug": "day-2"`), []byte(`"slug": "day-2-detour"`), 1)
	w := pushTrip(t, app, changed, "")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "leg 2 holds 1 post") {
		t.Fatalf("status %d, want 409 naming leg 2: %s", w.Code, w.Body.String())
	}
	onDisk, _ := os.ReadFile(tripPath(app.dataDir, fixtureSlug))
	if !bytes.Equal(onDisk, original) {
		t.Error("a refused re-push replaced the file on the volume")
	}
	if files := tripFiles(t, app); len(files) != 1 {
		t.Errorf("a refused re-push left %v", files)
	}
	if got := mustGet(t, app, post.ID); got.LegSlug != "day-2" {
		t.Errorf("the post's leg was re-routed: %+v", got)
	}
}

// --- what already existed, on a trip from the volume ---------------------------------------

// A pinned post from the road still replaces the plan with evidence on a trip
// that came from the volume and has options: the leg gets a driven track, the
// map payload and the open data carry it, and the arrival stamp path works.
func TestVolumeTripPinnedPostDrawsTheTrack(t *testing.T) {
	app := activeTripApp(t)
	legs := mustLegs(t, app, fixtureSlug)
	origin := cpByOrd(t, app, 1)
	night1 := cpByOrd(t, app, 3)

	// Arrive at the origin through the real route, with the token.
	target := "/admin/checkpoint/" + strconv.FormatInt(origin.ID, 10) + "/arrive"
	if w := postForm(t, app, target, url.Values{}); w.Code != http.StatusSeeOther {
		t.Fatalf("arrive at the origin: status %d: %s", w.Code, w.Body.String())
	}
	// A photo from the road: a pinned post on leg 1, well away from any
	// private place.
	lat, lng := 41.3, -101.3
	post := mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "photo", TZ: "America/Chicago", Title: "from the road",
		OccurredAt: "2030-05-01T18:00:00Z", Lat: &lat, Lng: &lng, PinSource: pinEXIF})

	rv, err := app.loadRide()
	if err != nil {
		t.Fatal(err)
	}
	posts, _ := app.store.Posts(fixtureSlug, 0)
	m := buildMap(rv.Legs, posts, legTracks(rv.Legs, posts))
	if len(m.Tracks) != 1 || m.Tracks[0].LegOrd != 1 {
		t.Fatalf("the map carries tracks %+v; want one, for leg 1", m.Tracks)
	}
	track := m.Tracks[0].Line
	if len(track) != 2 || track[0][0] != fixtureStart.Lng || track[0][1] != fixtureStart.Lat || track[1][0] != lng || track[1][1] != lat {
		t.Errorf("track %v; want the snapped origin, then the post's pin", track)
	}
	if len(m.Posts) != 1 || m.Posts[0].ID != post.ID || m.Posts[0].When == "" {
		t.Errorf("the map's posts: %+v", m.Posts)
	}
	// The page embeds the same payload, and the open data says the same.
	home := send(t, app, "GET", "/", "", "", nil).Body.String()
	if !strings.Contains(home, `"tracks":[{"leg":1`) {
		t.Error("window.RIDE on / has no track for leg 1")
	}
	geo := send(t, app, "GET", "/route.geojson", "", "", nil).Body.String()
	if !strings.Contains(geo, `"kind": "track"`) || !strings.Contains(geo, `"made_it": true`) {
		t.Error("/route.geojson lacks the track or the arrival")
	}

	// "Made it to": adopting a lodging post's pin moves a public checkpoint to
	// where the car stopped and stamps it.
	mlat, mlng := 42.0312, -102.0177
	lodging := mustCreate(t, app, &Post{LegID: legs[0].ID, CheckpointID: night1.ID, Kind: "lodging", TZ: "America/Chicago",
		Title: "the room", OccurredAt: "2030-05-02T01:00:00Z", Lat: &mlat, Lng: &mlng, PinSource: pinEXIF})
	target = "/admin/checkpoint/" + strconv.FormatInt(night1.ID, 10) + "/arrive"
	if w := postForm(t, app, target, url.Values{"post_id": {strconv.FormatInt(lodging.ID, 10)}}); w.Code != http.StatusSeeOther {
		t.Fatalf("made it to: status %d: %s", w.Code, w.Body.String())
	}
	got := cpByOrd(t, app, 3)
	if got.ArrivedAt == "" || got.Lat != mlat || got.Lng != mlng || got.Precision != precisionExact {
		t.Errorf("night 1 after made-it-to: %+v", got)
	}
	// A boot pass over the volume leaves the arrived checkpoint and the posts alone.
	if loaded, failed := loadVolumeTrips(app.store, app.dataDir); loaded != 1 || failed != 0 {
		t.Fatalf("boot pass: %d loaded, %d failed", loaded, failed)
	}
	if again := cpByOrd(t, app, 3); again.Lat != mlat || again.ArrivedAt != got.ArrivedAt || again.Precision != precisionExact {
		t.Errorf("a boot pass moved the arrived checkpoint: %+v", again)
	}
	if c := rideCensus(t, app, fixtureSlug); c.Posts != 2 || c.Arrived != 2 {
		t.Errorf("after the boot pass: %+v", c)
	}
	// The private origin kept its cell through its own arrival.
	if o := cpByOrd(t, app, 1); o.Lat != fixtureStart.Lat || o.Lng != fixtureStart.Lng || o.Precision != precisionFuzzed {
		t.Errorf("the origin after arriving: %+v", o)
	}
}

// One pin alone is a pin, not a track — the floor legTracks has always had.
// Stated here because it is the gap in "a photo replaces the plan": the first
// photo of a day draws no track until the day's origin is stamped arrived or
// a second pin lands.
func TestVolumeTripOnePinAloneIsNotATrack(t *testing.T) {
	app := activeTripApp(t)
	legs := mustLegs(t, app, fixtureSlug)
	lat, lng := 41.3, -101.3
	mustCreate(t, app, &Post{LegID: legs[0].ID, Kind: "photo", TZ: "America/Chicago",
		Lat: &lat, Lng: &lng, PinSource: pinEXIF})
	posts, _ := app.store.Posts(fixtureSlug, 0)
	if tracks := legTracks(legs, posts); len(tracks) != 0 {
		t.Errorf("one pin made a track: %v", tracks)
	}
}

// While the whole site is private, the planner is as closed as every other
// page: the plan, its alternatives and its dates are not for a visitor who
// has not signed in.
func TestPlannerIsClosedWhileTheSiteIsPrivate(t *testing.T) {
	app := activeTripApp(t)
	app.sitePrivate = true
	for _, p := range []string{"/plan", "/plan.json", "/plan.json?opt-finish=stopover"} {
		w := send(t, app, "GET", p, "", "", nil)
		if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/admin/login") {
			t.Errorf("%s while private: status %d, Location %q; want a 302 to the login", p, w.Code, w.Header().Get("Location"))
		}
		if strings.Contains(w.Body.String(), "Alder") {
			t.Errorf("%s while private carries the plan", p)
		}
		if w := send(t, app, "GET", p, testToken, "", nil); w.Code != http.StatusOK {
			t.Errorf("%s while private, with the token: status %d", p, w.Code)
		}
	}
	if w := send(t, app, "GET", "/healthz", "", "", nil); w.Code != http.StatusOK {
		t.Errorf("/healthz while private: status %d", w.Code)
	}
}
