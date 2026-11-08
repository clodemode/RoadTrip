package main

import (
	"strings"
	"testing"
	"time"
)

// Kris, 2026-11-07: "Start with a Start Drive button." Until the trip's start
// carries its departure stamp, the home page and the driver screen lead with
// one large Start the drive button; once the drive has started it is gone.
func TestStartTheDriveButtonLeadsUntilTheDriveStarts(t *testing.T) {
	tt := newTT(t)
	app, kris := freeApp(t, tt)
	const btn = `class="btn btn-primary btn-lg start-drive" href="/driver/post"`
	for _, path := range []string{"/", "/driver"} {
		if body := doAs(t, app, kris, "GET", path, nil).Body.String(); !strings.Contains(body, btn) {
			t.Fatalf("%s before the start: no Start the drive button", path)
		}
	}
	if body := doAs(t, app, nil, "GET", "/", nil).Body.String(); strings.Contains(body, btn) {
		t.Fatal("a visitor who is not signed in must not see the Start the drive button")
	}
	rv, err := app.loadRide()
	if err != nil {
		t.Fatal(err)
	}
	st := tripStart(rv.Legs)
	if st == nil {
		t.Fatal("fixture has no start")
	}
	if err := app.store.SetArrived(st.ID, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/driver"} {
		if body := doAs(t, app, kris, "GET", path, nil).Body.String(); strings.Contains(body, btn) {
			t.Fatalf("%s after the start: the Start the drive button is still there", path)
		}
	}
}
