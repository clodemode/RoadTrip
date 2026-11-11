package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A whole-window load that still carries embed=1 lost its player frame: it is
// sent to the clean address, which serves the shell and the card. A request
// that does not say where it loads keeps the frame behaviour it always had.
// (features/play_tap_never_sticks.feature proves the same through the mux.)
func TestShellSendsALostFrameBackToTheCleanAddress(t *testing.T) {
	app := chesterApp(t, newAPIStub(t))
	app.tracks = newRoadtracks(rtKey, newMO(t).srv.URL)
	kris := asUser(t, app, "kris", roleEditor)
	rv, err := app.loadRide()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetArrived(tripStart(rv.Legs).ID, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	get := func(target, dest string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", target, nil)
		if dest != "" {
			req.Header.Set("Sec-Fetch-Dest", dest)
		}
		req.AddCookie(kris)
		w := httptest.NewRecorder()
		app.routes().ServeHTTP(w, req)
		return w
	}
	if w := get("/plan?embed=1&x=2", "document"); w.Code != 302 || w.Header().Get("Location") != "/plan?x=2" {
		t.Fatalf("a whole-window load with embed=1: %d to %q", w.Code, w.Header().Get("Location"))
	}
	if w := get("/plan", "document"); w.Code != 200 || !strings.Contains(w.Body.String(), `id="rt-player"`) {
		t.Fatalf("the clean address is not the shell: %d", w.Code)
	}
	if w := get("/plan?embed=1", ""); w.Code != 200 || strings.Contains(w.Body.String(), `id="rt-player"`) {
		t.Fatalf("no Sec-Fetch-Dest changed today's frame behaviour: %d", w.Code)
	}
	if w := get("/plan?embed=1", "iframe"); w.Code != 200 || strings.Contains(w.Body.String(), `id="rt-player"`) {
		t.Fatalf("a frame navigation got the shell: %d", w.Code)
	}
}
