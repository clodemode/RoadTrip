package main

import (
	"net/http"
	"strings"
	"testing"
)

// The checklist and the GeoJSON are for signed-in drivers only (Kris, 2026-11-07).
func TestChecklistAndGeoJSONAreDriverOnly(t *testing.T) {
	app := activeTripApp(t)
	if w := pushChecklist(t, app, fixtureChecklist(t), ""); w.Code != http.StatusOK {
		t.Fatalf("push: %d", w.Code)
	}
	// A visitor: no link anywhere, and the content itself is refused.
	home := send(t, app, "GET", "/", "", "", nil).Body.String()
	for _, link := range []string{`href="/checklist"`, `href="/route.geojson"`, "of 5 done"} {
		if strings.Contains(home, link) {
			t.Errorf("a visitor's front page carries %q", link)
		}
	}
	for _, p := range []string{"/checklist", "/checklist.json", "/route.geojson"} {
		w := send(t, app, "GET", p, "", "", nil)
		if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/driver/login") {
			t.Errorf("visitor %s: status %d location %q, want a redirect to sign-in", p, w.Code, w.Header().Get("Location"))
		}
		if strings.Contains(w.Body.String(), "canoe") || strings.Contains(w.Body.String(), "FeatureCollection") {
			t.Errorf("visitor %s carries content", p)
		}
	}
	// A signed-in driver (the push token is the same credential): links and 200s.
	home = send(t, app, "GET", "/", testToken, "", nil).Body.String()
	for _, want := range []string{`href="/checklist"`, `href="/route.geojson"`, "of 5 done"} {
		if !strings.Contains(home, want) {
			t.Errorf("a signed-in front page lacks %q", want)
		}
	}
	for _, p := range []string{"/checklist", "/checklist.json", "/route.geojson"} {
		if w := send(t, app, "GET", p, testToken, "", nil); w.Code != http.StatusOK {
			t.Errorf("signed-in %s: status %d, want 200", p, w.Code)
		}
	}
}
