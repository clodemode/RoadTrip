package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/cucumber/godog"
)

// Steps for features/every_stop_is_navigable.feature,
// features/private_overlay_push_fails_closed.feature and
// features/trip_readiness.feature. They drive the real mux the way the
// trip-keeper's carriage does: the push token for the pushes, a signed-in
// session for the pages. The trips are the synthetic fixtures with their
// navigate marks stripped, so every stop starts with no address. Every place
// and address is invented.
type navigableWorld struct {
	t      *testing.T
	app    *App
	cookie *http.Cookie
	raw    []byte
	slug   string
	last   *httptest.ResponseRecorder
	body   map[string]any
	// before is the overlay file as it was before a push, "" when absent.
	before string
	pages  map[string]string
}

var quotedRe = regexp.MustCompile(`"([^"]*)"`)

func initNavigableScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &navigableWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = navigableWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^the trip-keeper pushes with the push token and is signed in$`, func() {
		w.app = tokenApp(t, testToken)
		w.cookie = asUser(t, w.app, "kris", roleAdmin)
	})

	// --- the trip document -----------------------------------------------
	sc.Step(`^a trip document whose stops carry no address and no navigate mark$`, func() error {
		return w.load("trip-fixture.geojson", fixtureSlug)
	})
	sc.Step(`^a trip document with routing points whose stops carry no address and no navigate mark$`, func() error {
		return w.load("routing-trip-fixture.geojson", "routing-fixture")
	})
	sc.Step(`^the stop "([^"]*)" is marked navigate by point$`, func(name string) error {
		old := []byte(`"name": "` + name + `",`)
		if !bytes.Contains(w.raw, old) {
			return fmt.Errorf("the document has no stop named %q", name)
		}
		w.raw = bytes.Replace(w.raw, old, []byte(`"name": "`+name+`", "nav": "point",`), 1)
		return nil
	})
	sc.Step(`^the trip is pushed$`, func() error { w.push(""); return nil })
	sc.Step(`^the trip is pushed and made active$`, func() error { w.push("?activate=1"); return nil })
	sc.Step(`^the trip was made the site's ride before this rule, and the drive has started$`, func() error {
		// A ride that went live before the rule: stored, then made the site's
		// ride directly, as an old volume holds it. The check applies at push
		// and activation, never to what is already there.
		w.push("")
		if w.last.Code != 200 {
			return fmt.Errorf("the push answered %d", w.last.Code)
		}
		if err := w.app.store.SetMeta(metaActiveRide, w.slug); err != nil {
			return err
		}
		rv, err := w.app.loadRide()
		if err != nil {
			return err
		}
		return w.app.store.SetArrived(tripStart(rv.Legs).ID, time.Now().Add(-time.Hour), nil)
	})
	sc.Step(`^the trip-keeper makes the trip active by name$`, func() error {
		w.last = postForm(t, w.app, "/driver/ride/active", url.Values{"slug": {w.slug}})
		w.body = decode(w.last)
		return nil
	})

	// --- the answer ----------------------------------------------------------
	sc.Step(`^the trip-keeper's push is taken$`, func() error { return w.code(http.StatusOK) })
	sc.Step(`^the trip-keeper's push is refused with a (\d+)$`, func(n int) error { return w.code(n) })
	sc.Step(`^the activation is refused with a 409$`, func() error { return w.code(http.StatusConflict) })
	sc.Step(`^the activation is taken$`, func() error { return w.code(http.StatusOK) })
	sc.Step(`^the answer lists (.+) as needing an address$`, func(list string) error {
		got := w.needs()
		want := []string{}
		for _, m := range quotedRe.FindAllStringSubmatch(list, -1) {
			want = append(want, m[1])
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			return fmt.Errorf("needs_address is %v, want %v (answer: %s)", got, want, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^a re-push of the trip with activate answers an empty list of stops needing an address$`, func() error {
		w.push("?activate=1")
		if err := w.code(http.StatusOK); err != nil {
			return err
		}
		if got := w.needs(); len(got) != 0 {
			return fmt.Errorf("needs_address is %v, want none", got)
		}
		if _, ok := w.body["needs_address"].([]any); !ok {
			return fmt.Errorf("needs_address is not a list: %s", w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the answer does not list "([^"]*)"$`, func(name string) error {
		for _, n := range w.needs() {
			if n == name {
				return fmt.Errorf("the answer lists %q", name)
			}
		}
		return nil
	})
	sc.Step(`^the answer lists at least one stop$`, func() error {
		// Positive control: the check below could fire, the stops are listed.
		if len(w.needs()) == 0 {
			return fmt.Errorf("the answer lists no stop at all: %s", w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the trip is on the site's books$`, func() error {
		if got := rideCensus(t, w.app, w.slug).Checkpoints; got == 0 {
			return fmt.Errorf("the trip %s is not stored", w.slug)
		}
		return nil
	})
	sc.Step(`^the site is not showing the trip$`, func() error {
		if w.app.activeSlug() == w.slug {
			return fmt.Errorf("the site is showing %s", w.slug)
		}
		return nil
	})
	sc.Step(`^the site is showing the trip$`, func() error {
		if w.app.activeSlug() != w.slug {
			return fmt.Errorf("the site shows %s, not %s", w.app.activeSlug(), w.slug)
		}
		return nil
	})

	// --- the pages -------------------------------------------------------------
	sc.Step(`^the trip-keeper opens the driver screen and the Ride page$`, func() error {
		w.pages = map[string]string{}
		for _, p := range []string{"/driver", "/", "/driver/hud.json"} {
			rec := doAs(t, w.app, w.cookie, "GET", p, nil)
			if rec.Code != 200 {
				return fmt.Errorf("%s answered %d", p, rec.Code)
			}
			w.pages[p] = rec.Body.String()
		}
		return nil
	})
	sc.Step(`^neither carries a Navigate link to any navigator$`, func() error {
		// Positive control: the pages are the real ones, with the next stop on them.
		if !strings.Contains(w.pages["/driver"], "Birch Gate") || !strings.Contains(w.pages["/"], "Birch Gate") {
			return fmt.Errorf("the pages do not name the next stop, so the check below could not fire")
		}
		for p, body := range w.pages {
			for _, host := range []string{"waze.com/ul", "google.com/maps/dir", "maps.apple.com/?daddr"} {
				// The driver screen's hidden-by-default template slot has href="#".
				if strings.Contains(body, host) {
					return fmt.Errorf("%s carries a %s link", p, host)
				}
			}
		}
		return nil
	})
	sc.Step(`^the driver screen tells the trip-keeper "([^"]*)"$`, func(s string) error {
		if !strings.Contains(w.pages["/driver"], s) {
			return fmt.Errorf("the driver screen does not say %q", s)
		}
		if !strings.Contains(w.pages["/driver/hud.json"], `"nav_none": true`) && !strings.Contains(w.pages["/driver/hud.json"], `"nav_none":true`) {
			return fmt.Errorf("hud.json does not say there is no navigation: %s", w.pages["/driver/hud.json"])
		}
		return nil
	})
	sc.Step(`^the Ride page says "([^"]*)" for the day's end and for the stops$`, func(s string) error {
		page := w.pages["/"]
		if !strings.Contains(page, `class="dayend card"`) {
			return fmt.Errorf("the Ride page has no day's end block")
		}
		de := dayEndRe.FindString(page)
		if !strings.Contains(de, s) {
			return fmt.Errorf("the day's end does not say %q", s)
		}
		if n := strings.Count(page, s); n < 2 {
			return fmt.Errorf("the Ride page says %q %d time(s), want the day's end and a stop row", s, n)
		}
		return nil
	})

	// --- the overlay -----------------------------------------------------------
	sc.Step(`^the overlay is pushed by ride path with addresses for (.+)$`, func(list string) error {
		cps := map[string]privateCheckpoint{}
		for i, m := range quotedRe.FindAllStringSubmatch(list, -1) {
			ord, err := w.ordOf(m[1])
			if err != nil {
				return err
			}
			cps[strconv.Itoa(ord)] = privateCheckpoint{Address: strconv.Itoa(10+i) + " Larkspur Row, Pemberwick", For: m[1]}
		}
		w.pushOverlay(cps)
		return w.code(http.StatusOK)
	})
	sc.Step(`^the overlay is pushed by ride path with the entry (\d+) for "([^"]*)" at "([^"]*)"$`, func(ord int, forName, addr string) error {
		w.before = w.overlayFile()
		w.pushOverlay(map[string]privateCheckpoint{strconv.Itoa(ord): {Address: addr, For: forName}})
		return nil
	})
	sc.Step(`^the overlay is pushed with the ride query parameter$`, func() error {
		w.before = w.overlayFile()
		w.last = send(t, w.app, "POST", "/driver/private?ride="+w.slug, testToken, "application/json",
			[]byte(`{"checkpoints":{"2":{"address":"9 Larkspur Row, Pemberwick"}}}`))
		return nil
	})
	sc.Step(`^the first ride's block holds an entry at "([^"]*)"$`, func(addr string) error {
		rec := send(t, w.app, "POST", "/driver/private", testToken, "application/json",
			[]byte(`{"checkpoints":{"3":{"address":`+strconv.Quote(addr)+`}}}`))
		if rec.Code != 200 {
			return fmt.Errorf("filing the first ride's entry answered %d %s", rec.Code, rec.Body.String())
		}
		return nil
	})
	sc.Step(`^the answer names entry (\d+) and "([^"]*)"$`, func(ord int, forName string) error {
		b := w.last.Body.String()
		if !strings.Contains(b, "entry "+strconv.Itoa(ord)) || !strings.Contains(b, forName) {
			return fmt.Errorf("the refusal does not name entry %d and %q: %s", ord, forName, b)
		}
		return nil
	})
	sc.Step(`^the answer names entry (\d+)$`, func(ord int) error {
		if b := w.last.Body.String(); !strings.Contains(b, "entry "+strconv.Itoa(ord)) {
			return fmt.Errorf("the refusal does not name entry %d: %s", ord, b)
		}
		return nil
	})
	sc.Step(`^the answer names the path "([^"]*)"$`, func(p string) error {
		if b := w.last.Body.String(); !strings.Contains(b, p) {
			return fmt.Errorf("the refusal does not name %q: %s", p, b)
		}
		return nil
	})
	sc.Step(`^the answer does not carry "([^"]*)"$`, func(s string) error {
		// Positive control: the refusal is a real, wordy one.
		if len(w.last.Body.String()) < 20 {
			return fmt.Errorf("the refusal says too little for the check to mean anything: %q", w.last.Body.String())
		}
		if carries(w.last.Body.String(), s) {
			return fmt.Errorf("the refusal carries the address")
		}
		return nil
	})
	sc.Step(`^the overlay on the volume is as it was$`, func() error {
		if now := w.overlayFile(); now != w.before {
			return fmt.Errorf("a refused push changed the overlay")
		}
		return nil
	})
	sc.Step(`^the trip's block holds only the address "([^"]*)" at (\d+)$`, func(addr string, ord int) error {
		pd, err := loadPrivate(w.app.dataDir)
		if err != nil {
			return err
		}
		got := pd.forRide(w.slug, w.app.rideSlug).byOrd()
		if len(got) != 1 || got[ord].Address != addr {
			return fmt.Errorf("the trip's block is %d entry(ies), entry %d reads %q", len(got), ord, got[ord].Address)
		}
		return nil
	})
	sc.Step(`^the first ride's block still holds "([^"]*)"$`, func(addr string) error {
		pd, err := loadPrivate(w.app.dataDir)
		if err != nil {
			return err
		}
		for _, e := range pd.forRide(w.app.rideSlug, w.app.rideSlug).Checkpoints {
			if e.Address == addr {
				return nil
			}
		}
		return fmt.Errorf("the first ride's block lost its entry")
	})

	// --- readiness ---------------------------------------------------------------
	sc.Step(`^the trip-keeper opens the readiness page$`, func() error {
		rec := doAs(t, w.app, w.cookie, "GET", "/driver/ready", nil)
		if rec.Code != 200 {
			return fmt.Errorf("/driver/ready answered %d", rec.Code)
		}
		w.pages = map[string]string{"/driver/ready": rec.Body.String()}
		return nil
	})
	sc.Step(`^a visitor opens the readiness page$`, func() error {
		w.last = doAs(t, w.app, nil, "GET", "/driver/ready", nil)
		return nil
	})
	sc.Step(`^the visitor is not given the page$`, func() error {
		if w.last.Code == http.StatusOK {
			return fmt.Errorf("a visitor was given the readiness page")
		}
		return nil
	})
	sc.Step(`^the visitor's answer carries no stop name$`, func() error {
		// Positive control: the signed-in page does name the stops.
		rec := doAs(t, w.app, w.cookie, "GET", "/driver/ready", nil)
		if !strings.Contains(rec.Body.String(), "Birch Gate") {
			return fmt.Errorf("the signed-in page does not name Birch Gate, so the check could not fire")
		}
		if strings.Contains(w.last.Body.String(), "Birch Gate") {
			return fmt.Errorf("the visitor's answer names a stop")
		}
		return nil
	})
	sc.Step(`^it lists the stops in order: (.+)$`, func(list string) error {
		page := w.pages["/driver/ready"]
		at := -1
		for _, m := range quotedRe.FindAllStringSubmatch(list, -1) {
			i := strings.Index(page, "<b>"+m[1]+"</b>")
			if i < 0 || i < at {
				return fmt.Errorf("%q is missing or out of order on the readiness page", m[1])
			}
			at = i
		}
		return nil
	})
	sc.Step(`^the row for "([^"]*)" says Navigate goes to its address and offers Waze, Google Maps and Apple Maps$`, func(name string) error {
		row, err := w.row(name)
		if err != nil {
			return err
		}
		pd, _ := loadPrivate(w.app.dataDir)
		var addr string
		for _, e := range pd.forRide(w.slug, w.app.rideSlug).Checkpoints {
			if e.For == name {
				addr = e.Address
			}
		}
		if addr == "" || !strings.Contains(row, "Navigate goes to "+addr) {
			return fmt.Errorf("the row for %s does not say it goes to %q: %s", name, addr, row)
		}
		for _, host := range []string{"waze.com/ul", "google.com/maps/dir", "maps.apple.com/?daddr"} {
			if !strings.Contains(row, host) {
				return fmt.Errorf("the row for %s has no %s link", name, host)
			}
		}
		return nil
	})
	sc.Step(`^the row for "([^"]*)" says Navigate goes by point$`, func(name string) error {
		row, err := w.row(name)
		if err != nil {
			return err
		}
		if !strings.Contains(row, "(by point)") {
			return fmt.Errorf("the row for %s does not say by point: %s", name, row)
		}
		return nil
	})
	sc.Step(`^the row for "([^"]*)" says it is the start$`, func(name string) error {
		row, err := w.row(name)
		if err != nil {
			return err
		}
		if !strings.Contains(row, "start, not navigated to") {
			return fmt.Errorf("the row for %s does not say it is the start", name)
		}
		return nil
	})
	sc.Step(`^the trip-keeper ticks "([^"]*)" as checked$`, func(name string) error {
		ord, err := w.ordOf(name)
		if err != nil {
			return err
		}
		rec := doAs(t, w.app, w.cookie, "POST", "/driver/ready/stop/"+strconv.Itoa(ord), url.Values{"checked": {"1"}})
		if rec.Code != http.StatusSeeOther {
			return fmt.Errorf("the tick answered %d", rec.Code)
		}
		return nil
	})
	sc.Step(`^the row for "([^"]*)" is checked$`, func(name string) error {
		row, err := w.row(name)
		if err != nil {
			return err
		}
		if !strings.Contains(row, `class="ready-ok"`) {
			return fmt.Errorf("the row for %s is not checked", name)
		}
		return nil
	})
	sc.Step(`^the row for "([^"]*)" is not checked$`, func(name string) error {
		row, err := w.row(name)
		if err != nil {
			return err
		}
		if strings.Contains(row, `class="ready-ok"`) || !strings.Contains(row, "not checked") {
			return fmt.Errorf("the row for %s is checked", name)
		}
		return nil
	})
}

// load reads a fixture and strips its navigate marks, so every stop starts
// with no address and no mark.
func (w *navigableWorld) load(file, slug string) error {
	raw, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		return err
	}
	w.raw = bytes.ReplaceAll(raw, []byte(`, "nav": "point",`), []byte(`,`))
	w.slug = slug
	return nil
}

func (w *navigableWorld) push(query string) {
	w.last = pushTrip(w.t, w.app, w.raw, query)
	w.body = decode(w.last)
}

func decode(rec *httptest.ResponseRecorder) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return m
}

func (w *navigableWorld) code(want int) error {
	if w.last.Code != want {
		return fmt.Errorf("the answer is %d, want %d: %s", w.last.Code, want, w.last.Body.String())
	}
	return nil
}

func (w *navigableWorld) needs() []string {
	out := []string{}
	if l, ok := w.body["needs_address"].([]any); ok {
		for _, v := range l {
			out = append(out, fmt.Sprint(v))
		}
	}
	return out
}

func (w *navigableWorld) pushOverlay(cps map[string]privateCheckpoint) {
	raw, _ := json.Marshal(map[string]any{"checkpoints": cps})
	w.last = send(w.t, w.app, "POST", "/driver/private/ride/"+w.slug, testToken, "application/json", raw)
}

func (w *navigableWorld) overlayFile() string {
	raw, _ := os.ReadFile(filepath.Join(w.app.dataDir, privateFile))
	return string(raw)
}

// ordOf is the number a stop holds in the pushed trip.
func (w *navigableWorld) ordOf(name string) (int, error) {
	legs, err := w.app.store.Legs(w.slug)
	if err != nil {
		return 0, err
	}
	for _, l := range legs {
		for _, c := range l.Checkpoints {
			if c.Name == name {
				return c.Ord, nil
			}
		}
	}
	return 0, fmt.Errorf("no stop named %q in %s", name, w.slug)
}

// row is one stop's list item on the readiness page.
func (w *navigableWorld) row(name string) (string, error) {
	page := w.pages["/driver/ready"]
	i := strings.Index(page, "<b>"+name+"</b>")
	if i < 0 {
		return "", fmt.Errorf("the readiness page has no row for %s", name)
	}
	rest := page[i:]
	if j := strings.Index(rest, "</li>"); j >= 0 {
		rest = rest[:j]
	}
	return rest, nil
}
