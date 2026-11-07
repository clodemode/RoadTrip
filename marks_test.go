package main

import (
	"strings"
	"testing"
)

func TestMarkCheckpointsLettersForLandmarksNumbersForLegEnds(t *testing.T) {
	legs := []Leg{
		{Ord: 1, Checkpoints: []Checkpoint{{Ord: 1, IsTerminal: true}, {Ord: 2}, {Ord: 3, IsTerminal: true}}},
		{Ord: 2, Checkpoints: []Checkpoint{{Ord: 4}, {Ord: 5, IsTerminal: true}}},
	}
	markCheckpoints(legs)
	var got []string
	for _, l := range legs {
		for _, c := range l.Checkpoints {
			got = append(got, c.Mark)
		}
	}
	if want := "1 A 1 B 2"; strings.Join(got, " ") != want {
		t.Fatalf("marks %q, want %q", strings.Join(got, " "), want)
	}
	if letterMark(0) != "A" || letterMark(25) != "Z" || letterMark(26) != "AA" || letterMark(27) != "AB" {
		t.Fatalf("letters: %s %s %s %s", letterMark(0), letterMark(25), letterMark(26), letterMark(27))
	}
}

func TestPagesAndMapDataUseTheMarks(t *testing.T) {
	app := activeTripApp(t)
	rv, err := app.loadRide()
	if err != nil {
		t.Fatal(err)
	}
	var landmark, legEnd *Checkpoint
	for i := range rv.Legs {
		for j := range rv.Legs[i].Checkpoints {
			c := &rv.Legs[i].Checkpoints[j]
			if !c.IsTerminal && landmark == nil {
				landmark = c
			}
			if c.IsTerminal && legEnd == nil {
				legEnd = c
			}
		}
	}
	if landmark == nil || legEnd == nil {
		t.Skip("fixture has no landmark and leg end")
	}
	if len(landmark.Mark) == 0 || landmark.Mark[0] < 'A' || landmark.Mark[0] > 'Z' {
		t.Fatalf("landmark mark %q is not a letter", landmark.Mark)
	}
	page := doAs(t, app, nil, "GET", "/", nil).Body.String()
	if !strings.Contains(page, `<span class="cp-n">`+landmark.Mark+`</span>`) {
		t.Errorf("index lacks the landmark letter %q", landmark.Mark)
	}
	if !strings.Contains(page, `"mark":"`+landmark.Mark+`"`) {
		t.Errorf("the map data on the page lacks the landmark mark")
	}
	gj := doAs(t, app, nil, "GET", "/route.geojson", nil).Body.String()
	if !strings.Contains(gj, `"mark": "`+landmark.Mark+`"`) {
		t.Errorf("geojson lacks the landmark mark")
	}
	js := doAs(t, app, nil, "GET", "/static/js/map.js", nil).Body.String()
	if !strings.Contains(js, "c.mark") {
		t.Error("map.js does not label by mark")
	}
}
