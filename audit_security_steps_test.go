package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for the four features the 2026-11-16 audit left behind:
// ride_assignment_holds, sign_in_hardening, security_headers and
// privacy_seams. Every request goes through the real mux.

type auditWorld struct {
	t     *testing.T
	app   *App
	users map[string]*http.Cookie
	rec   *httptest.ResponseRecorder
	recs  []*httptest.ResponseRecorder

	second  string // the ride the site does not show
	before  int    // posts on the shown ride before the request
	post    *Post
	posts   []*Post
	devices map[string]*http.Cookie

	door   LatLng
	cell   LatLng
	pin    pinResult
	cps    []Checkpoint
	orig   string
	thumb  string
	melted string
}

func initAuditSecurityScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &auditWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = auditWorld{t: t, users: map[string]*http.Cookie{}, devices: map[string]*http.Cookie{}}
		return ctx, nil
	})

	// --- ride assignment ---------------------------------------------------
	sc.Step(`^a site showing the fixture ride, with a second ride in its database$`, func() error {
		w.app = activeTripApp(t)
		w.second = w.app.rideSlug
		if w.app.activeSlug() == w.second {
			return fmt.Errorf("the fixture ride is not the one shown")
		}
		w.users["admin"] = asUser(t, w.app, "root", roleAdmin)
		return nil
	})
	sc.Step(`^driver ann is assigned to the ride the site shows$`, func() error {
		w.users["ann"] = asUser(t, w.app, "ann", roleEditor)
		return w.app.auth.users.setRides("ann", []string{w.app.activeSlug()})
	})
	sc.Step(`^driver bob is assigned only to the second ride$`, func() error {
		w.users["bob"] = asUser(t, w.app, "bob", roleEditor)
		return w.app.auth.users.setRides("bob", []string{w.second})
	})
	sc.Step(`^bob sends a new post to "([^"]*)"$`, func(addr string) error {
		w.before = w.shownPosts()
		addr = strings.ReplaceAll(addr, "SECOND", w.second)
		w.rec = doAs(t, w.app, w.users["bob"], "POST", addr, url.Values{"kind": {"note"}, "title": {"written by bob"}, "body": {"x"}})
		return nil
	})
	sc.Step(`^the site answers (\d+)$`, func(code int) error {
		if w.rec.Code != code {
			return fmt.Errorf("answered %d, want %d: %s", w.rec.Code, code, firstLine(w.rec.Body.String()))
		}
		return nil
	})
	sc.Step(`^the ride the site shows has no new post$`, func() error {
		if n := w.shownPosts(); n != w.before {
			return fmt.Errorf("the shown ride had %d post(s) and now has %d", w.before, n)
		}
		return nil
	})
	sc.Step(`^a post on the second ride$`, func() error { return w.makePost(w.second) })
	sc.Step(`^a post on the ride the site shows$`, func() error { return w.makePost(w.app.activeSlug()) })
	sc.Step(`^ann sends "([^"]*)" to that post's "([^"]*)"$`, func(method, action string) error {
		form := url.Values{"kind": {"note"}, "title": {"rewritten by ann"}, "pin_source": {"none"}, "leg_id": {fmt.Sprint(w.post.LegID)}}
		if method == "GET" {
			form = nil
		}
		w.rec = doAs(t, w.app, w.users["ann"], method, fmt.Sprintf("/driver/post/%d/%s", w.post.ID, action), form)
		return nil
	})
	sc.Step(`^that post is unchanged$`, func() error {
		p, err := w.app.store.Post(w.post.ID)
		if err != nil {
			return fmt.Errorf("the post is gone: %v", err)
		}
		if p.Title != w.post.Title || p.PinFuzzed != w.post.PinFuzzed || p.PinSource != w.post.PinSource ||
			(p.Lat == nil) != (w.post.Lat == nil) || (p.Lat != nil && *p.Lat != *w.post.Lat) {
			return fmt.Errorf("the post changed: %+v", p)
		}
		return nil
	})
	sc.Step(`^that post is gone$`, func() error {
		if _, err := w.app.store.Post(w.post.ID); err == nil {
			return fmt.Errorf("the post is still there")
		}
		return nil
	})
	sc.Step(`^ann sends "([^"]*)" to "([^"]*)"$`, func(method, addr string) error {
		w.rec = doAs(t, w.app, w.users["ann"], method, addr, nil)
		return nil
	})
	notRefused := func(rec *httptest.ResponseRecorder, who string) error {
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized || rec.Code == http.StatusFound {
			return fmt.Errorf("%s was refused: %d", who, rec.Code)
		}
		return nil
	}
	sc.Step(`^the push token sending "([^"]*)" to "([^"]*)" is not refused$`, func(method, addr string) error {
		return notRefused(send(t, w.app, method, addr, testToken, "", nil), "the push token")
	})
	sc.Step(`^an admin sending "([^"]*)" to "([^"]*)" is not refused$`, func(method, addr string) error {
		return notRefused(doAs(t, w.app, w.users["admin"], method, addr, nil), "an admin")
	})

	// --- sign-in -----------------------------------------------------------
	sc.Step(`^a site with user ann whose password is known$`, func() error {
		w.app = activeTripApp(t)
		w.app.melt = newMeltSinks("", "test", filepath.Join(t.TempDir(), "melt", "roadtrip.jsonl"))
		w.users["ann"] = asUser(t, w.app, "ann", roleEditor) // password "pw-ann"
		w.users["admin"] = asUser(t, w.app, "root", roleAdmin)
		return nil
	})
	sc.Step(`^ann signs in with next "([^"]*)"$`, func(next string) error {
		next = strings.ReplaceAll(next, "<TAB>", "\t")
		w.rec = w.signIn("ann", "pw-ann", "198.51.100.1", url.Values{"next": {next}})
		return nil
	})
	sc.Step(`^she lands on "([^"]*)"$`, func(want string) error {
		if w.rec.Code != http.StatusFound {
			return fmt.Errorf("the sign-in answered %d", w.rec.Code)
		}
		if got := w.rec.Header().Get("Location"); got != want {
			return fmt.Errorf("sent to %q, want %q", got, want)
		}
		return nil
	})
	sc.Step(`^five wrong passwords for ann arrive from one address$`, func() error {
		for i := 0; i < 5; i++ {
			w.recs = append(w.recs, w.signIn("ann", fmt.Sprintf("wrong-%d", i), "198.51.100.7", nil))
		}
		return nil
	})
	sc.Step(`^each of them is answered 401$`, func() error {
		for i, r := range w.recs {
			if r.Code != http.StatusUnauthorized {
				return fmt.Errorf("wrong password %d answered %d", i+1, r.Code)
			}
		}
		return nil
	})
	sc.Step(`^a sixth sign-in for ann arrives from that address with the right password$`, func() error {
		w.rec = w.signIn("ann", "pw-ann", "198.51.100.7", nil)
		return nil
	})
	sc.Step(`^no session is issued$`, func() error {
		if c := sessionOf(w.rec); c != nil {
			return fmt.Errorf("a session cookie was set")
		}
		return nil
	})
	sc.Step(`^ann signs in from another address with the right password$`, func() error {
		w.rec = w.signIn("ann", "pw-ann", "203.0.113.9", nil)
		return nil
	})
	sc.Step(`^a session is issued$`, func() error {
		if c := sessionOf(w.rec); c == nil {
			return fmt.Errorf("no session cookie: status %d", w.rec.Code)
		}
		return nil
	})
	sc.Step(`^a sign-in arrives with a 64 KB password$`, func() error {
		w.rec = w.signIn("ann", strings.Repeat("a", 64<<10), "198.51.100.1", nil)
		return nil
	})
	sc.Step(`^ann is signed in on her phone and on her laptop$`, func() error {
		for i, d := range []string{"phone", "laptop"} {
			rec := w.signIn("ann", "pw-ann", fmt.Sprintf("198.51.100.%d", 20+i), nil)
			c := sessionOf(rec)
			if c == nil {
				return fmt.Errorf("could not sign in on the %s: %d", d, rec.Code)
			}
			w.devices[d] = c
			if !w.opensDriver(c) {
				return fmt.Errorf("the %s's fresh session does not open the driver page", d)
			}
		}
		return nil
	})
	sc.Step(`^ann signs out on her phone$`, func() error {
		w.rec = doAs(t, w.app, w.devices["phone"], "POST", "/driver/logout", url.Values{})
		return nil
	})
	sc.Step(`^an admin resets ann's password$`, func() error {
		w.rec = doAs(t, w.app, w.users["admin"], "POST", "/driver/users/ann/reset", url.Values{"password": {"a-new-password"}})
		if w.rec.Code >= 400 {
			return fmt.Errorf("the reset answered %d", w.rec.Code)
		}
		return nil
	})
	sc.Step(`^her (phone|laptop)'s session no longer opens the driver page$`, func(d string) error {
		if w.opensDriver(w.devices[d]) {
			return fmt.Errorf("the %s's old session still opens the driver page", d)
		}
		return nil
	})
	sc.Step(`^a wrong sign-in arrives for the name "([^"]*)"$`, func(name string) error {
		w.melted = name
		w.rec = w.signIn(name, "nope", "198.51.100.1", nil)
		return nil
	})
	sc.Step(`^the record of it carries no typed name$`, func() error {
		for i := 0; i < 100; i++ {
			rec := doAs(t, w.app, w.users["admin"], "GET", "/driver/melt.json?stream=all&n=200&event=login", nil)
			var doc struct {
				Records []map[string]any `json:"records"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &doc)
			if len(doc.Records) > 0 {
				if strings.Contains(rec.Body.String(), w.melted) {
					return fmt.Errorf("the bus carries the typed name: %s", rec.Body.String())
				}
				return nil
			}
			time.Sleep(20 * time.Millisecond)
		}
		return fmt.Errorf("no login record reached the bus")
	})
	sc.Step(`^driver ann reads "([^"]*)"$`, func(addr string) error {
		w.rec = doAs(t, w.app, w.users["ann"], "GET", addr, nil)
		return nil
	})

	// --- headers -----------------------------------------------------------
	sc.Step(`^a visitor asks for "([^"]*)"$`, func(addr string) error {
		if w.app == nil {
			w.app = activeTripApp(t)
		}
		w.rec = doAs(t, w.app, nil, "GET", addr, nil)
		return nil
	})
	header := func(name, want string) error {
		if got := w.rec.Header().Get(name); got != want {
			return fmt.Errorf("%s is %q, want %q", name, got, want)
		}
		return nil
	}
	sc.Step(`^the answer says to stay on https for a year$`, func() error {
		return header("Strict-Transport-Security", "max-age=31536000")
	})
	sc.Step(`^the answer says not to guess the content type$`, func() error {
		return header("X-Content-Type-Options", "nosniff")
	})
	sc.Step(`^the answer keeps the full address from other sites$`, func() error {
		return header("Referrer-Policy", "strict-origin-when-cross-origin")
	})
	sc.Step(`^the answer may be framed by this site only$`, func() error {
		if err := header("X-Frame-Options", "SAMEORIGIN"); err != nil {
			return err
		}
		return header("Content-Security-Policy", "frame-ancestors 'self'")
	})
	sc.Step(`^the player shell's frame asks for "([^"]*)"$`, func(addr string) error {
		w.app = activeTripApp(t)
		req := httptest.NewRequest("GET", addr, nil)
		req.Header.Set("Sec-Fetch-Dest", "iframe")
		req.AddCookie(asUser(t, w.app, "ann", roleEditor))
		w.rec = httptest.NewRecorder()
		w.app.routes().ServeHTTP(w.rec, req)
		return nil
	})
	sc.Step(`^a file "([^"]*)" is sitting in the media folder$`, func(name string) error {
		w.app = activeTripApp(t)
		if err := os.MkdirAll(w.app.mediaDir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(w.app.mediaDir, name), []byte("an upload that has not been scrubbed"), 0o644)
	})

	// --- privacy seams -----------------------------------------------------
	sc.Step(`^a private stop and a phone standing at its door$`, func() error { return w.privateStop(0) })
	sc.Step(`^a private stop and a phone standing 30 km from it$`, func() error { return w.privateStop(30) })
	pinWith := func(form url.Values) error {
		form.Set("lat", fmt.Sprint(w.door.Lat))
		form.Set("lng", fmt.Sprint(w.door.Lng))
		form.Set("pin_source", pinBrowser)
		req := httptest.NewRequest("POST", "/driver/post", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := req.ParseForm(); err != nil {
			return err
		}
		w.pin = resolvePin(pinInputFrom(req, nil, nil, nil), w.cps, pinPrecisionSnap)
		if w.pin.Lat == nil {
			return fmt.Errorf("no pin came out: %s", w.pin.Note)
		}
		return nil
	}
	sc.Step(`^a post is pinned there with the fuzz turned off$`, func() error {
		return pinWith(url.Values{"fuzz_choice": {"1"}})
	})
	sc.Step(`^a post is pinned there with the fuzz turned off and the exact spot confirmed$`, func() error {
		return pinWith(url.Values{"fuzz_choice": {"1"}, "exact_at_private": {"yes"}})
	})
	sc.Step(`^the published pin is the private stop's cell, not the door$`, func() error {
		if !w.pin.Fuzzed || *w.pin.Lat != w.cell.Lat || *w.pin.Lng != w.cell.Lng {
			return fmt.Errorf("published %v, %v (fuzzed %v), want the cell %v", *w.pin.Lat, *w.pin.Lng, w.pin.Fuzzed, w.cell)
		}
		return nil
	})
	atDoor := func() error {
		if w.pin.Fuzzed || *w.pin.Lat != w.door.Lat || *w.pin.Lng != w.door.Lng {
			return fmt.Errorf("published %v, %v (fuzzed %v), want %v", *w.pin.Lat, *w.pin.Lng, w.pin.Fuzzed, w.door)
		}
		return nil
	}
	sc.Step(`^the published pin is the door$`, atDoor)
	sc.Step(`^the published pin is where the phone stood$`, atDoor)

	sc.Step(`^a photo post whose photo is served on the public media path$`, func() error { return w.photoPosts(1) })
	sc.Step(`^two posts that show the same photo$`, func() error { return w.photoPosts(2) })
	sc.Step(`^the trip-keeper deletes (?:that post|the first of them)$`, func() error {
		w.rec = doAs(t, w.app, w.users["admin"], "POST", fmt.Sprintf("/driver/post/%d/delete", w.posts[0].ID), url.Values{})
		if w.rec.Code != http.StatusSeeOther {
			return fmt.Errorf("the delete answered %d", w.rec.Code)
		}
		return nil
	})
	served := func(rel string) int { return doAs(t, w.app, nil, "GET", "/media/"+rel, nil).Code }
	sc.Step(`^the photo is no longer served$`, func() error {
		if c := served(w.orig); c != http.StatusNotFound {
			return fmt.Errorf("/media/%s answers %d after the delete", w.orig, c)
		}
		return nil
	})
	sc.Step(`^the thumbnail is no longer served$`, func() error {
		if c := served(w.thumb); c != http.StatusNotFound {
			return fmt.Errorf("/media/%s answers %d after the delete", w.thumb, c)
		}
		return nil
	})
	sc.Step(`^the photo is still served$`, func() error {
		if c := served(w.orig); c != http.StatusOK {
			return fmt.Errorf("/media/%s answers %d, want 200", w.orig, c)
		}
		return nil
	})
	sc.Step(`^the trip-keeper asks for the route data$`, func() error {
		w.app = activeTripApp(t)
		w.rec = doAs(t, w.app, asUser(t, w.app, "root", roleAdmin), "GET", "/route.geojson", nil)
		return nil
	})
	sc.Step(`^the answer is marked private and not to be stored$`, func() error {
		if w.rec.Code != http.StatusOK {
			return fmt.Errorf("answered %d", w.rec.Code)
		}
		if got := w.rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			return fmt.Errorf("the route data is offered to other origins: %q", got)
		}
		return header("Cache-Control", "private, no-store")
	})
	sc.Step(`^a visitor asks for a media file that is not there$`, func() error {
		w.app = activeTripApp(t)
		w.rec = doAs(t, w.app, nil, "GET", "/media/2026/11/0000000000000000.jpg", nil)
		if w.rec.Code != http.StatusNotFound {
			return fmt.Errorf("answered %d", w.rec.Code)
		}
		return nil
	})
	sc.Step(`^the answer carries no long-lived cache header$`, func() error {
		if cc := w.rec.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") || strings.Contains(cc, "max-age=31536000") {
			return fmt.Errorf("a 404 is cached: %q", cc)
		}
		return nil
	})
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

func sessionOf(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" && c.MaxAge >= 0 {
			return c
		}
	}
	return nil
}

func (w *auditWorld) shownPosts() int {
	n := 0
	for _, l := range mustLegs(w.t, w.app, w.app.activeSlug()) {
		ps, err := w.app.store.Posts(w.app.activeSlug(), l.ID)
		if err != nil {
			w.t.Fatal(err)
		}
		n += len(ps)
	}
	return n
}

// makePost writes a pinned note on the first leg of a ride. It carries a kept
// exact point, so an Unfuzz that got through would show.
func (w *auditWorld) makePost(ride string) error {
	legs := mustLegs(w.t, w.app, ride)
	lat, lng := 35.25, -101.75
	p := &Post{LegID: legs[0].ID, Kind: "note", Title: "left as it was", TZ: "America/Chicago",
		OccurredAt: utcString(time.Date(2026, 9, 5, 18, 0, 0, 0, time.UTC)), Lat: &lat, Lng: &lng, PinSource: pinBrowser}
	if err := w.app.store.CreatePost(p); err != nil {
		return err
	}
	got, err := w.app.store.Post(p.ID)
	if err != nil {
		return err
	}
	w.post = got
	return nil
}

// signIn posts the sign-in form from one address, the way Fly's edge names it.
func (w *auditWorld) signIn(user, pw, addr string, extra url.Values) *httptest.ResponseRecorder {
	form := url.Values{"username": {user}, "password": {pw}}
	for k, v := range extra {
		form[k] = v
	}
	req := httptest.NewRequest("POST", "/driver/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Fly-Client-IP", addr)
	rec := httptest.NewRecorder()
	w.app.routes().ServeHTTP(rec, req)
	return rec
}

func (w *auditWorld) opensDriver(c *http.Cookie) bool {
	return doAs(w.t, w.app, c, "GET", "/driver", nil).Code == http.StatusOK
}

// privateStop is a synthetic private place and a phone km north of its door.
func (w *auditWorld) privateStop(km float64) error {
	door := LatLng{Lat: 40.1234, Lng: -100.4321}
	c := Checkpoint{ID: 7, Ord: 1, Name: "synthetic private place", Lat: door.Lat, Lng: door.Lng, Precision: precisionFuzzed}
	c.fuzz()
	w.cps = []Checkpoint{c}
	w.cell = LatLng{Lat: c.Lat, Lng: c.Lng}
	// Four decimals, which is what a browser fix is kept to.
	w.door = LatLng{Lat: math.Round((door.Lat+km/kmPerDegLat)*1e4) / 1e4, Lng: door.Lng}
	return nil
}

// photoPosts writes n posts on the shown ride that all show one photo, whose
// two files sit on the public media path.
func (w *auditWorld) photoPosts(n int) error {
	w.app = activeTripApp(w.t)
	w.users["admin"] = asUser(w.t, w.app, "root", roleAdmin)
	sha := fmt.Sprintf("%016x%048x", 0xa0d17, 0)
	w.orig, w.thumb = "2026/11/"+sha[:16]+".jpg", "2026/11/"+sha[:16]+"_t.jpg"
	for _, rel := range []string{w.orig, w.thumb} {
		full := filepath.Join(w.app.mediaDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte("not a real picture"), 0o644); err != nil {
			return err
		}
	}
	ph := &Photo{Path: w.orig, ThumbPath: w.thumb, TZ: "America/Chicago", Width: 800, Height: 600, SHA256: sha}
	if err := w.app.store.CreatePhoto(ph); err != nil {
		return err
	}
	legs := mustLegs(w.t, w.app, w.app.activeSlug())
	for i := 0; i < n; i++ {
		p := &Post{LegID: legs[0].ID, Kind: "photo", Title: fmt.Sprintf("photo %d", i+1), TZ: "America/Chicago",
			OccurredAt: utcString(time.Now().Add(-time.Hour)), PhotoID: ph.ID, PinSource: pinNone}
		if err := w.app.store.CreatePost(p); err != nil {
			return err
		}
		w.posts = append(w.posts, p)
	}
	if c := doAs(w.t, w.app, nil, "GET", "/media/"+w.orig, nil).Code; c != http.StatusOK {
		return fmt.Errorf("the photo is not served to begin with: %d", c)
	}
	return nil
}
