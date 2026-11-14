package main

import (
	"net/http"
	"strings"
	"testing"
)

// The GeoJSON is for signed-in drivers only (Kris, 2026-11-07).
func TestGeoJSONIsDriverOnly(t *testing.T) {
	app := activeTripApp(t)
	// A visitor: no link anywhere, and the content itself is refused.
	home := send(t, app, "GET", "/", "", "", nil).Body.String()
	if strings.Contains(home, `href="/route.geojson"`) {
		t.Errorf("a visitor's front page links the GeoJSON")
	}
	w := send(t, app, "GET", "/route.geojson", "", "", nil)
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/driver/login") {
		t.Errorf("visitor /route.geojson: status %d location %q, want a redirect to sign-in", w.Code, w.Header().Get("Location"))
	}
	if strings.Contains(w.Body.String(), "FeatureCollection") {
		t.Errorf("visitor /route.geojson carries content")
	}
	// A signed-in driver (the push token is the same credential): a 200.
	if w := send(t, app, "GET", "/route.geojson", testToken, "", nil); w.Code != http.StatusOK {
		t.Errorf("signed-in /route.geojson: status %d, want 200", w.Code)
	}
}
