package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The push token is a second way through the ONE gate, not a second gate. What
// these tests pin down is exactly that: it opens whatever the session opens
// (an admin POST, and /route.geojson while the whole site is private), a wrong
// value opens nothing, an unset PUSH_TOKEN makes the header meaningless, and
// the cookie carries on working beside it.

const testToken = "0123456789abcdef01234567"

// tokenApp is testApp with PUSH_TOKEN set in the environment, because that is
// where NewAuth reads it and "unset" is a case worth being able to test.
func tokenApp(t *testing.T, token string) *App {
	t.Helper()
	t.Setenv("PUSH_TOKEN", token)
	return testApp(t)
}

// bearer runs a request through the real mux — requireAuth and the visibility
// gate included, since the wiring is the point here.
func bearer(t *testing.T, app *App, method, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, req)
	return w
}

// An admin POST from curl, with no cookie and no sign-in: the whole reason the
// token exists.
func TestPushTokenOpensAnAdminPost(t *testing.T) {
	app := tokenApp(t, testToken)
	cp := firstCheckpoint(t, testLegs(t, app))
	target := "/driver/checkpoint/" + strconv.FormatInt(cp.ID, 10) + "/arrive"

	if w := bearer(t, app, "POST", target, testToken); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303: %s", w.Code, w.Body.String())
	}
	got, err := app.store.Checkpoint(cp.ID)
	if err != nil || got.ArrivedAt == "" {
		t.Errorf("the stamp did not land: %v %+v", err, got)
	}
}

func TestPushTokenWrongValueIsRefused(t *testing.T) {
	app := tokenApp(t, testToken)
	cp := firstCheckpoint(t, testLegs(t, app))
	target := "/driver/checkpoint/" + strconv.FormatInt(cp.ID, 10) + "/arrive"

	if w := bearer(t, app, "POST", target, "0123456789abcdef0123456X"); w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
	if w := bearer(t, app, "POST", target, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no header: status %d, want 401", w.Code)
	}
	if got, _ := app.store.Checkpoint(cp.ID); got.ArrivedAt != "" {
		t.Error("a refused request still stamped the checkpoint")
	}
}

// With PUSH_TOKEN unset the header path does not exist — a request carrying
// any bearer value is exactly as unauthenticated as one carrying none.
func TestPushTokenUnsetIgnoresTheHeader(t *testing.T) {
	app := tokenApp(t, "")
	cp := firstCheckpoint(t, testLegs(t, app))
	target := "/driver/checkpoint/" + strconv.FormatInt(cp.ID, 10) + "/arrive"

	if w := bearer(t, app, "POST", target, testToken); w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
	// The empty token must never compare equal to an empty header either.
	if w := bearer(t, app, "POST", target, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no header: status %d, want 401", w.Code)
	}
}

// While the site is private the GeoJSON is behind the same gate as everything
// else, and the token has to reach it — otherwise the route data can only be
// read from a browser, which is not where a push runs.
func TestPushTokenReachesRouteGeoJSONWhilePrivate(t *testing.T) {
	app := tokenApp(t, testToken)
	app.sitePrivate = true

	w := bearer(t, app, "GET", "/route.geojson", testToken)
	if w.Code != 200 {
		t.Fatalf("status %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"FeatureCollection"`) {
		t.Errorf("body is not the GeoJSON: %.120s", w.Body.String())
	}
	// Anonymous stays shut: the visibility gate redirects to the login.
	if w := bearer(t, app, "GET", "/route.geojson", ""); w.Code != http.StatusFound {
		t.Errorf("anonymous status %d, want 302 to the login", w.Code)
	}
}

// The cookie is the person's credential and the token must not have disturbed
// it — with or without PUSH_TOKEN set.
func TestSessionCookieStillWorks(t *testing.T) {
	for _, token := range []string{testToken, ""} {
		app := tokenApp(t, token)
		rec := httptest.NewRecorder()
		app.auth.Issue(rec, false, testAdminUser(t, app))
		cookie := rec.Result().Cookies()[0]

		req := httptest.NewRequest("GET", "/driver/posts.json", nil)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		app.routes().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Errorf("PUSH_TOKEN %q: cookie status %d, want 200", token, w.Code)
		}
		// A tampered cookie is still refused, token or no token.
		bad := *cookie
		bad.Value = "nonsense"
		req = httptest.NewRequest("GET", "/driver/posts.json", nil)
		req.AddCookie(&bad)
		w = httptest.NewRecorder()
		app.routes().ServeHTTP(w, req)
		if w.Code != http.StatusFound {
			t.Errorf("PUSH_TOKEN %q: bad cookie status %d, want 302 to the login", token, w.Code)
		}
	}
}

// The scheme is matched case-insensitively (RFC 7235 says so) but the token
// itself is not, and stray whitespace around it is tolerated the way a shell
// variable expansion can leave it.
func TestPushTokenHeaderShapes(t *testing.T) {
	app := tokenApp(t, testToken)
	cases := []struct {
		header string
		want   bool
	}{
		{"Bearer " + testToken, true},
		{"bearer " + testToken, true},
		{"Bearer  " + testToken + " ", true},
		{"Bearer " + strings.ToUpper(testToken), false},
		{"Basic " + testToken, false},
		{testToken, false},
		{"Bearer", false},
		{"Bearer ", false},
		{"", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/driver/posts.json", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		if got := app.auth.pushAuthed(req); got != c.want {
			t.Errorf("pushAuthed(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}
