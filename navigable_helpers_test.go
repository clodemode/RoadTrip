package main

import (
	"encoding/json"
	"testing"
)

// markNavigableByPoint marks every stop of a ride navigate-by-point, as a trip
// document's "nav": "point" does, so a test about something else may read the
// links a stop with a target carries (navstop.go). The embedded seed ride is
// history and carries no mark of its own.
func markNavigableByPoint(t *testing.T, app *App, slug string) {
	t.Helper()
	legs, err := app.store.Legs(slug)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, l := range legs {
		for _, c := range l.Checkpoints {
			names = append(names, c.Name)
		}
	}
	raw, _ := json.Marshal(names)
	if err := app.store.SetMeta(navPointKey(slug), string(raw)); err != nil {
		t.Fatal(err)
	}
}
