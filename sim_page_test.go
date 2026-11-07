package main

import (
	"strings"
	"testing"
)

// The simulated drive must replace navigator.geolocation before hud.js asks for
// it, so sim.js has to come first in the /driver page.
func TestDriverPageLoadsSimBeforeHUD(t *testing.T) {
	app, _ := hudRide(t)
	c := asUser(t, app, "kris", roleAdmin)
	w := doAs(t, app, c, "GET", "/driver", nil)
	if w.Code != 200 {
		t.Fatalf("/driver: %d", w.Code)
	}
	body := w.Body.String()
	sim, hud := strings.Index(body, "/static/js/sim.js"), strings.Index(body, "/static/js/hud.js")
	if sim < 0 {
		t.Fatal("/driver does not load sim.js")
	}
	if hud < 0 || sim > hud {
		t.Fatalf("sim.js must load before hud.js (sim at %d, hud at %d)", sim, hud)
	}
}
