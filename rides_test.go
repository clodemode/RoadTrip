package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Drivers are assigned to rides (Kris, 2026-11-07). The active ride of
// activeTripApp is fixtureSlug; the other ride in its database is app.rideSlug.

func ridesApp(t *testing.T) (app *App, ann, bob, admin *http.Cookie) {
	t.Helper()
	app = activeTripApp(t)
	if w := pushChecklist(t, app, fixtureChecklist(t), ""); w.Code != http.StatusOK {
		t.Fatalf("push checklist: %d", w.Code)
	}
	ann = asUser(t, app, "ann", roleEditor)
	bob = asUser(t, app, "bob", roleEditor)
	admin = asUser(t, app, "root", roleAdmin)
	if err := app.auth.users.setRides("ann", []string{fixtureSlug}); err != nil {
		t.Fatal(err)
	}
	if err := app.auth.users.setRides("bob", []string{app.rideSlug}); err != nil {
		t.Fatal(err)
	}
	return
}

// A driver's reads and writes on a ride they are not on are refused; on their
// own ride they work; an admin and the push token are on every ride.
func TestDriverIsRefusedOnARideTheyAreNotAssignedTo(t *testing.T) {
	app, ann, bob, admin := ridesApp(t)
	reads := []string{"/checklist", "/checklist.json", "/route.geojson", "/driver/post", "/driver/posts.json", "/driver/lodging"}
	for _, p := range reads {
		if w := doAs(t, app, ann, "GET", p, nil); w.Code != http.StatusOK {
			t.Errorf("ann (on the ride) GET %s: %d, want 200", p, w.Code)
		}
		if w := doAs(t, app, bob, "GET", p, nil); w.Code != http.StatusForbidden {
			t.Errorf("bob (not on the ride) GET %s: %d, want 403", p, w.Code)
		}
		if w := doAs(t, app, admin, "GET", p, nil); w.Code != http.StatusOK {
			t.Errorf("admin GET %s: %d, want 200", p, w.Code)
		}
		if w := send(t, app, "GET", p, testToken, "", nil); w.Code != http.StatusOK {
			t.Errorf("push token GET %s: %d, want 200", p, w.Code)
		}
	}
	writes := []string{"/driver/post", "/driver/lodging", "/driver/plan", "/driver/checkpoint/1/arrive",
		"/driver/post/1/delete", "/driver/post/1/pin", "/driver/post/1/edit", "/driver/checklist/step/x"}
	for _, p := range writes {
		if w := doAs(t, app, bob, "POST", p, url.Values{"x": {"1"}}); w.Code != http.StatusForbidden {
			t.Errorf("bob POST %s: %d, want 403", p, w.Code)
		}
		if w := doAs(t, app, ann, "POST", p, url.Values{"x": {"1"}}); w.Code == http.StatusForbidden {
			t.Errorf("ann POST %s: refused as not her ride", p)
		}
	}
	if w := doAs(t, app, bob, "GET", "/driver/post/1/edit", nil); w.Code != http.StatusForbidden {
		t.Errorf("bob GET post edit: %d, want 403", w.Code)
	}
}

// Naming the other ride does not get round it: ?ride= and the form's ride.
func TestDriverCannotReachAnotherRideByNamingIt(t *testing.T) {
	app, ann, bob, _ := ridesApp(t)
	other := app.rideSlug
	if w := doAs(t, app, ann, "GET", "/driver/checklist.json?ride="+other, nil); w.Code != http.StatusForbidden {
		t.Errorf("ann reading ride %s by name: %d, want 403", other, w.Code)
	}
	if w := doAs(t, app, ann, "POST", "/driver/checklist/step/x", url.Values{"ride": {other}}); w.Code != http.StatusForbidden {
		t.Errorf("ann marking on ride %s by name: %d, want 403", other, w.Code)
	}
	// Bob is on the other ride: naming it is allowed (it may 404 for want of a checklist, never 403).
	if w := doAs(t, app, bob, "GET", "/driver/checklist.json?ride="+other, nil); w.Code == http.StatusForbidden {
		t.Errorf("bob reading his own ride by name was refused")
	}
}

// The nav and tool bar show a driver only what their ride has.
func TestNavShowsOnlyTheDriversRides(t *testing.T) {
	app, ann, bob, admin := ridesApp(t)
	tools := []string{`href="/checklist"`, `href="/driver/post"`, `href="/driver/lodging"`}
	for _, c := range []struct {
		who  string
		ck   *http.Cookie
		want bool
	}{{"ann", ann, true}, {"bob", bob, false}, {"admin", admin, true}} {
		home := doAs(t, app, c.ck, "GET", "/", nil).Body.String()
		for _, l := range tools {
			if got := strings.Contains(home, l); got != c.want {
				t.Errorf("%s front page has %s = %v, want %v", c.who, l, got, c.want)
			}
		}
	}
}

// A driver with no assignment list (written before assignment) keeps the active
// ride; boot pins that list; a later switch of the active ride then refuses them.
func TestLegacyUserKeepsTheActiveRideAndIsPinned(t *testing.T) {
	app := activeTripApp(t)
	pushChecklist(t, app, fixtureChecklist(t), "")
	old := asUser(t, app, "old", roleEditor)
	if u, _ := app.auth.users.get("old"); u.Rides != nil {
		t.Fatalf("a fresh user has a list: %v", u.Rides)
	}
	if w := doAs(t, app, old, "GET", "/checklist", nil); w.Code != http.StatusOK {
		t.Fatalf("legacy user on the active ride: %d, want 200", w.Code)
	}
	n, err := app.auth.users.migrateLegacyRides(app.activeSlug())
	if err != nil || n != 1 {
		t.Fatalf("migrate = %d, %v; want 1 user pinned", n, err)
	}
	if u, _ := app.auth.users.get("old"); len(u.Rides) != 1 || u.Rides[0] != fixtureSlug {
		t.Fatalf("pinned to %v, want [%s]", u.Rides, fixtureSlug)
	}
	if n, _ := app.auth.users.migrateLegacyRides(app.activeSlug()); n != 0 {
		t.Errorf("second migrate pinned %d, want 0", n)
	}
	postForm(t, app, "/driver/ride/active", url.Values{"slug": {app.rideSlug}})
	if w := doAs(t, app, old, "GET", "/driver/lodging", nil); w.Code != http.StatusForbidden {
		t.Errorf("after the active ride moved, the pinned user: %d, want 403", w.Code)
	}
}

// Assignments survive a restart: the file carries the list, an empty list stays empty.
func TestRideAssignmentsPersist(t *testing.T) {
	dir := t.TempDir()
	s := newUserStore(filepath.Join(dir, usersFile))
	if err := s.create("ann", "h", roleEditor); err != nil {
		t.Fatal(err)
	}
	if err := s.create("zed", "h", roleEditor); err != nil {
		t.Fatal(err)
	}
	if err := s.setRides("ann", []string{"b", "a", "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.setRides("zed", nil); err != nil {
		t.Fatal(err)
	}
	s2 := newUserStore(filepath.Join(dir, usersFile))
	if err := s2.load(); err != nil {
		t.Fatal(err)
	}
	a, _ := s2.get("ann")
	z, _ := s2.get("zed")
	if strings.Join(a.Rides, ",") != "a,b" {
		t.Errorf("ann rides = %v, want [a b]", a.Rides)
	}
	if z.Rides == nil || len(z.Rides) != 0 {
		t.Errorf("zed rides = %#v, want an empty non-nil list", z.Rides)
	}
	if !a.mayRide("a", "x") || a.mayRide("x", "x") || !(User{Role: roleAdmin}).mayRide("anything", "x") {
		t.Error("mayRide: assigned yes, unassigned no, admin always")
	}
	_ = os.Remove(filepath.Join(dir, usersFile))
}

// /driver/users: an admin assigns rides; an editor cannot; unknown rides are refused.
func TestAdminAssignsRidesOnTheUsersPage(t *testing.T) {
	app, ann, bob, admin := ridesApp(t)
	page := doAs(t, app, admin, "GET", "/driver/users", nil).Body.String()
	if !strings.Contains(page, fixtureSlug) || !strings.Contains(page, app.rideSlug) {
		t.Errorf("users page does not list the rides")
	}
	set := func(c *http.Cookie, rides ...string) *httpResult {
		w := doAs(t, app, c, "POST", "/driver/users/bob/rides", url.Values{"ride": rides})
		return &httpResult{w.Code}
	}
	if r := set(ann, fixtureSlug); r.code != http.StatusForbidden {
		t.Errorf("an editor assigning rides: %d, want 403", r.code)
	}
	if r := set(admin, "no-such-ride"); r.code != http.StatusBadRequest {
		t.Errorf("assigning an unknown ride: %d, want 400", r.code)
	}
	if r := set(admin, fixtureSlug, app.rideSlug); r.code != http.StatusFound {
		t.Fatalf("assign: %d, want 302", r.code)
	}
	if w := doAs(t, app, bob, "GET", "/checklist", nil); w.Code != http.StatusOK {
		t.Errorf("bob after being assigned the active ride: %d, want 200", w.Code)
	}
	if r := set(admin); r.code != http.StatusFound {
		t.Fatalf("unassign all: %d", r.code)
	}
	if w := doAs(t, app, bob, "GET", "/checklist", nil); w.Code != http.StatusForbidden {
		t.Errorf("bob after being unassigned: %d, want 403", w.Code)
	}
}

type httpResult struct{ code int }

// FreeLance Destined has no ride: a driver on no ride still signs in, switches
// to it and gets its HUD, with none of the ride's data.
func TestFreeLanceStaysAvailableToADriverOnNoRide(t *testing.T) {
	app, _, _, _ := ridesApp(t)
	app.auth.users.create("nora", "h", roleEditor)
	app.auth.users.setRides("nora", nil)
	nora := asUser(t, app, "nora", roleEditor)
	if w := doAs(t, app, nora, "GET", "/driver/hud.json", nil); w.Code != http.StatusForbidden {
		t.Errorf("trip HUD for a driver on no ride: %d, want 403", w.Code)
	}
	if w := doAs(t, app, nora, "GET", "/driver", nil); w.Code != http.StatusOK {
		t.Fatalf("driver home for a driver on no ride: %d, want 200", w.Code)
	} else if strings.Contains(w.Body.String(), fixtureSlug) || strings.Contains(w.Body.String(), `href="/checklist"`) {
		t.Error("the home screen shows a ride the driver is not on")
	}
	if w := doAs(t, app, nora, "POST", "/driver/freelance/mode", url.Values{"mode": {"destined"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("freelance mode: %d, want 303", w.Code)
	}
	if w := doAs(t, app, nora, "GET", "/driver/hud.json", nil); w.Code != http.StatusOK {
		t.Errorf("destined HUD for a driver on no ride: %d, want 200", w.Code)
	} else if strings.Contains(w.Body.String(), `"leg_slug":"`+"l") && strings.Contains(w.Body.String(), `"day_total":`) && !strings.Contains(w.Body.String(), `"day_total":0`) {
		t.Error("destined HUD carries the ride's data")
	}
}
