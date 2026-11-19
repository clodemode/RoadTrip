package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The trip card's drawing and its address.
// spec-roadtrip-social-preview-cards-a-live-route-card-per-trip-the-photo-itself-on-a-photo-page-a-baked-odo-card-as-the-fallback-one-tag-block-and-a-check-that-reads-the-page-as-a-scraper-does.

// cardOut writes a card to $CARD_OUT/<name>.png when that is set, for a look.
func cardOut(t *testing.T, name string, b []byte) {
	t.Helper()
	if dir := os.Getenv("CARD_OUT"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, name+".png"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func checkCardPNG(t *testing.T, b []byte) {
	t.Helper()
	if len(b) >= 300*1024 {
		t.Errorf("card is %d bytes, want under 307200", len(b))
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	if cfg.Width != 1200 || cfg.Height != 630 {
		t.Errorf("card is %dx%d, want 1200x630", cfg.Width, cfg.Height)
	}
}

func fixtureCard(t *testing.T, file, slug string, stamp bool) (*App, *cardSpec) {
	t.Helper()
	app := tokenApp(t, testToken)
	raw, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	if w := pushTrip(t, app, raw, "?activate=1"); w.Code != 200 {
		t.Fatalf("push: %d %s", w.Code, w.Body.String())
	}
	if stamp {
		rv, err := app.loadRide()
		if err != nil {
			t.Fatal(err)
		}
		if err := app.store.SetArrived(tripStart(rv.Legs).ID, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	spec, _, err := app.rideCard(app.activeSlug())
	if err != nil {
		t.Fatalf("card for %s: %v", slug, err)
	}
	return app, spec
}

func TestTripCardForTheFixtureIsAPNGUnderBudgetAndSameBytesTwice(t *testing.T) {
	_, spec := fixtureCard(t, "trip-fixture.geojson", fixtureSlug, true)
	a, err := encodeCard(spec)
	if err != nil {
		t.Fatal(err)
	}
	checkCardPNG(t, a)
	b, _ := encodeCard(spec)
	if !bytes.Equal(a, b) {
		t.Error("two draws of the same spec differ")
	}
	cardOut(t, "fixture-on-the-road", a)
	if !strings.HasPrefix(spec.State, "On the road") || spec.Miles == 0 || spec.Days == 0 || spec.Dates == "" {
		t.Errorf("spec = %+v", spec)
	}
	dotted := 0
	for _, l := range spec.Legs {
		if l.Dotted {
			dotted++
		}
	}
	if dotted == 0 {
		t.Error("a trip on the road with legs ahead has no dotted leg")
	}
}

func TestTripCardPlannedDrawsEveryLegSolid(t *testing.T) {
	_, spec := fixtureCard(t, "trip-fixture.geojson", fixtureSlug, false)
	if spec.State != "Planned" {
		t.Errorf("state = %q", spec.State)
	}
	for _, l := range spec.Legs {
		if l.Dotted {
			t.Errorf("leg %d is dotted on a planned trip", l.Ord)
		}
	}
	b, err := encodeCard(spec)
	if err != nil {
		t.Fatal(err)
	}
	checkCardPNG(t, b)
	cardOut(t, "fixture-planned", b)
}

func TestTripCardForALoopTripDraws(t *testing.T) {
	_, spec := fixtureCard(t, "loop-trip-fixture.geojson", "loop", false)
	b, err := encodeCard(spec)
	if err != nil {
		t.Fatal(err)
	}
	checkCardPNG(t, b)
	cardOut(t, "loop", b)
}

func TestTripCardForAOneLegTripAndALongNameDraws(t *testing.T) {
	one := &cardSpec{Name: "A single day out", Miles: 212, Days: 1, Dates: "Sat Jun 1, 2030", State: "Planned",
		Legs: []cardLeg{{Ord: 1, Pts: [][2]float64{{-100, 40}, {-100.4, 40.3}, {-100.9, 40.4}}}}}
	b, err := encodeCard(one)
	if err != nil {
		t.Fatal(err)
	}
	checkCardPNG(t, b)
	cardOut(t, "one-leg", b)

	long := *one
	long.Name = "The Very Long Way Round From Alder Creek Through The Cedar Camp Ridge Country To Elm Hollow And Back Again"
	long.Legs = []cardLeg{{Ord: 1, Pts: [][2]float64{{-100, 40}, {-101, 41}}}, {Ord: 2, Dotted: true, Pts: [][2]float64{{-101, 41}, {-102, 41.2}}}}
	long.Discs = []cardDisc{{Ord: 1, Label: "d1", Pt: [2]float64{-101, 41}}, {Ord: 2, Label: "d2", Pt: [2]float64{-102, 41.2}}}
	size, lines := fitName(mustAssets(t).bold, long.Name, cardColumnW)
	if size < 56 || len(lines) > 2 {
		t.Errorf("name fit: size %v, %d lines", size, len(lines))
	}
	face := newFace(mustAssets(t).bold, size)
	for _, l := range lines {
		if w := textWidth(face, l, -size*0.03); w > cardColumnW {
			t.Errorf("line %q is %.0f px wide, column is %.0f", l, w, cardColumnW)
		}
	}
	b, err = encodeCard(&long)
	if err != nil {
		t.Fatal(err)
	}
	checkCardPNG(t, b)
	cardOut(t, "long-name", b)

	// a name with no spaces at all
	long.Name = strings.Repeat("Wanderlust", 12)
	if b, err = encodeCard(&long); err != nil {
		t.Fatal(err)
	}
	checkCardPNG(t, b)
}

func mustAssets(t *testing.T) *cardAssets {
	t.Helper()
	as, err := loadCardAssets()
	if err != nil {
		t.Fatal(err)
	}
	return as
}

// The card reads its colours from tokens baked out of app.css; a changed
// token in app.css without a re-bake fails here.
func TestCardTokensMatchAppCSS(t *testing.T) {
	css, err := os.ReadFile("static/css/app.css")
	if err != nil {
		t.Fatal(err)
	}
	as := mustAssets(t)
	for name, want := range map[string]string{"--leg-bg:": as.tok.LegBg, "--leg-bg-2:": as.tok.LegBg2, "--leg-ink:": as.tok.LegInk,
		"--leg-muted:": as.tok.LegMuted, "--leg-accent:": as.tok.LegAccent, "--leg-disc:": as.tok.LegDisc,
		"--rt-mast-from: ": as.tok.MastFrom, "--rt-mast-mid: ": as.tok.MastMid, "--rt-mast-to: ": as.tok.MastTo,
		"--rt-mast-edge: ": as.tok.MastEdge, "--rt-mast-ink: ": as.tok.MastInk, "--rt-mast-tag: ": as.tok.MastTag} {
		if !strings.Contains(string(css), name+want) {
			t.Errorf("app.css has no %s%s: re-bake cardlayers (tools/og_card/README.md)", name, want)
		}
	}
}

func TestTripCardHashIsStableCachedAndRaceFree(t *testing.T) {
	app, spec := fixtureCard(t, "trip-fixture.geojson", fixtureSlug, false)
	_, h1, _ := app.rideCard(app.activeSlug())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, h, err := app.rideCard(app.activeSlug())
			if err != nil || h != h1 {
				t.Errorf("hash %q err %v, want %q", h, err, h1)
			}
		}()
	}
	wg.Wait()
	if spec.hash() != h1 {
		t.Error("the spec's own hash is not the cached one")
	}
	moved := *spec
	moved.State = "Finished"
	if moved.hash() == h1 {
		t.Error("a changed state did not change the hash")
	}
}
