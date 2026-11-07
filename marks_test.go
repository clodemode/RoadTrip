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
	if want := "d1 A d1 B d2"; strings.Join(got, " ") != want {
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
	gj := send(t, app, "GET", "/route.geojson", testToken, "", nil).Body.String()
	if !strings.Contains(gj, `"mark": "`+landmark.Mark+`"`) {
		t.Errorf("geojson lacks the landmark mark")
	}
	js := doAs(t, app, nil, "GET", "/static/js/map.js", nil).Body.String()
	if !strings.Contains(js, "c.mark") {
		t.Error("map.js does not label by mark")
	}
}

func TestSignedInToolsAreSignedInOnly(t *testing.T) {
	app := chesterApp(t, newAPIStub(t))
	pat := asUser(t, app, "pat", roleEditor)
	for _, p := range []string{"/", "/plan", "/checklist", "/photos"} {
		anon := doAs(t, app, nil, "GET", p, nil).Body.String()
		for _, bad := range []string{"driver-bar", "waze.com", "/chester?q="} {
			if strings.Contains(anon, bad) {
				t.Errorf("%s: a visitor sees %q", p, bad)
			}
		}
		in := doAs(t, app, pat, "GET", p, nil).Body.String()
		for _, want := range []string{`class="driver-bar"`, `href="/driver/post"`, `href="/chester"`, `href="/plan"`, `href="/checklist"`} {
			if !strings.Contains(in, want) {
				t.Errorf("%s: signed-in bar lacks %s", p, want)
			}
		}
	}
	in := doAs(t, app, pat, "GET", "/", nil).Body.String()
	if !strings.Contains(in, "https://waze.com/ul?ll=") || !strings.Contains(in, "navigate=yes") {
		t.Error("index: no Waze button on the stops")
	}
	if !strings.Contains(in, "/chester?q=") {
		t.Error("index: no Ask Chester link on the stops")
	}
	// The chester page takes the prefill as text.
	pg := doAs(t, app, pat, "GET", "/chester?q=What+about+%3Cb%3EKingston%3C%2Fb%3E", nil).Body.String()
	if !strings.Contains(pg, "What about &lt;b&gt;Kingston&lt;/b&gt;") {
		t.Error("chester page does not prefill the question as escaped text")
	}
	// An editor's bar has no Users link; an admin's does.
	if strings.Contains(in, "/driver/users") {
		t.Error("editor sees the users link")
	}
	kris := asUser(t, app, "kris", roleAdmin)
	if !strings.Contains(doAs(t, app, kris, "GET", "/", nil).Body.String(), `href="/driver/users"`) {
		t.Error("admin lacks the users link")
	}
}

func TestWazeLinkShape(t *testing.T) {
	if got, want := wazeLink("Cedar Camp, ZZ", 0, 0), "https://waze.com/ul?navigate=yes&q=Cedar+Camp%2C+ZZ"; got != want {
		t.Errorf("q only: %q want %q", got, want)
	}
	if got, want := wazeLink("", 44.5, -76.25), "https://waze.com/ul?ll=44.5%2C-76.25&navigate=yes"; got != want {
		t.Errorf("ll only: %q want %q", got, want)
	}
}

func TestMapLinksGiveWazeGoogleAndAppleFromOneHelper(t *testing.T) {
	l := mapLinks(30.4, -97.9, "Old Mill Roasters")
	if l.Waze != wazeLink("Old Mill Roasters", 30.4, -97.9) {
		t.Errorf("waze: %q", l.Waze)
	}
	if want := "https://www.google.com/maps/dir/?api=1&destination=30.4,-97.9"; l.Google != want {
		t.Errorf("google: %q want %q", l.Google, want)
	}
	if want := "https://maps.apple.com/?daddr=30.4,-97.9&q=Old+Mill+Roasters"; l.Apple != want {
		t.Errorf("apple: %q want %q", l.Apple, want)
	}
	// No coordinates (a private, snapped place): the name goes, escaped, never a 0,0.
	n := mapLinks(0, 0, "Cedar Camp, ZZ & more")
	if strings.Contains(n.Google, "0,0") || strings.Contains(n.Apple, "0,0") ||
		n.Google != "https://www.google.com/maps/dir/?api=1&destination=Cedar+Camp%2C+ZZ+%26+more" ||
		n.Apple != "https://maps.apple.com/?daddr=Cedar+Camp%2C+ZZ+%26+more" {
		t.Errorf("name only: %q %q", n.Google, n.Apple)
	}
}

func TestMarkCheckpointsOneLegIsAllLetters(t *testing.T) {
	legs := []Leg{{Ord: 1, Checkpoints: []Checkpoint{{Ord: 1, IsTerminal: true}, {Ord: 2}, {Ord: 3, IsTerminal: true}}}}
	markCheckpoints(legs)
	var got []string
	for _, c := range legs[0].Checkpoints {
		got = append(got, c.Mark)
	}
	if want := "A B C"; strings.Join(got, " ") != want {
		t.Fatalf("one-leg marks %q, want %q", strings.Join(got, " "), want)
	}
}
