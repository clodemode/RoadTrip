package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/cucumber/godog"
)

// Steps for features/fuzz_one_kilometre.feature: the fuzz seam on synthetic
// points, with no server and no store round trip. A checkpoint is passed
// through Checkpoint.fuzz, which is what scanCheckpoint does on the way out.

type oneKmWorld struct {
	cps     []Checkpoint
	pubLat  float64 // the snapped cell point
	pubLng  float64
	pin     LatLng
	pinFrom LatLng
	got     LatLng
	taken   *Checkpoint
	line    [][]float64
	masked  [][]float64
}

const kmPerDegLat = 111.195

func initFuzzOneKilometreScenarios(sc *godog.ScenarioContext) {
	w := &oneKmWorld{}
	sc.Before(func(ctx context.Context, s *godog.Scenario) (context.Context, error) {
		*w = oneKmWorld{}
		return ctx, nil
	})
	sc.Step(`^a private checkpoint whose real point is ([-0-9.]+), ([-0-9.]+)$`, w.private)
	sc.Step(`^the store hands the checkpoint out$`, func() error { return nil })
	sc.Step(`^it is published at ([-0-9.]+), ([-0-9.]+)$`, w.cellIs)
	sc.Step(`^a pin ([0-9.]+) km north of the published place is published$`, w.publishPin)
	sc.Step(`^the pin is published at its own point, rounded to three decimals$`, w.ownPoint)
	sc.Step(`^the pin is not taken for the private place$`, func() error { return w.takenIs(false) })
	sc.Step(`^the pin is published at ([-0-9.]+), ([-0-9.]+)$`, w.pinCellIs)
	sc.Step(`^the pin is taken for the private place$`, func() error { return w.takenIs(true) })
	sc.Step(`^a route line with vertices ([0-9.]+) km, ([0-9.]+) km and ([0-9.]+) km north of the published place$`, w.routeLine)
	sc.Step(`^the line is masked$`, w.maskIt)
	sc.Step(`^the line keeps its 1.5 km vertex$`, w.keepsFar)
	sc.Step(`^the line has no vertex nearer than 1 km except the place's snapped point$`, w.nothingNear)
}

func (w *oneKmWorld) private(lat, lng float64) error {
	c := Checkpoint{Name: "synthetic private place", Lat: lat, Lng: lng, Precision: precisionFuzzed}
	c.fuzz()
	w.cps = []Checkpoint{c}
	w.pubLat, w.pubLng = c.Lat, c.Lng
	return nil
}

func sameDeg(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func (w *oneKmWorld) cellIs(lat, lng float64) error {
	if !sameDeg(w.pubLat, lat) || !sameDeg(w.pubLng, lng) {
		return fmt.Errorf("the checkpoint is published at %v, %v, want %v, %v", w.pubLat, w.pubLng, lat, lng)
	}
	return nil
}

func (w *oneKmWorld) publishPin(km float64) error {
	w.pin = LatLng{Lat: w.pubLat + km/kmPerDegLat, Lng: w.pubLng}
	w.got, w.taken = publishPosition(w.pin.Lat, w.pin.Lng, w.cps)
	return nil
}

func (w *oneKmWorld) ownPoint() error {
	want := LatLng{Lat: math.Round(w.pin.Lat*1000) / 1000, Lng: math.Round(w.pin.Lng*1000) / 1000}
	if !sameDeg(w.got.Lat, want.Lat) || !sameDeg(w.got.Lng, want.Lng) {
		return fmt.Errorf("the pin was published at %v, want its own point %v", w.got, want)
	}
	return nil
}

func (w *oneKmWorld) takenIs(yes bool) error {
	if (w.taken != nil) != yes {
		return fmt.Errorf("pin taken for the private place: %v, want %v", w.taken != nil, yes)
	}
	return nil
}

func (w *oneKmWorld) pinCellIs(lat, lng float64) error {
	if !sameDeg(w.got.Lat, lat) || !sameDeg(w.got.Lng, lng) {
		return fmt.Errorf("the pin was published at %v, want the cell %v, %v", w.got, lat, lng)
	}
	return nil
}

func (w *oneKmWorld) routeLine(a, b, c float64) error {
	w.line = nil
	for _, km := range []float64{a, b, c} {
		w.line = append(w.line, []float64{w.pubLng, w.pubLat + km/kmPerDegLat})
	}
	return nil
}

func (w *oneKmWorld) maskIt() error {
	raw, _ := json.Marshal(w.line)
	return json.Unmarshal(maskLine(raw, fuzzedOf(w.cps)), &w.masked)
}

func (w *oneKmWorld) keepsFar() error {
	for _, p := range w.masked {
		if sameDeg(p[1], w.line[0][1]) && sameDeg(p[0], w.line[0][0]) {
			return nil
		}
	}
	return fmt.Errorf("the 1.5 km vertex was hidden: %v", w.masked)
}

func (w *oneKmWorld) nothingNear() error {
	for _, p := range w.masked {
		if sameDeg(p[1], w.pubLat) && sameDeg(p[0], w.pubLng) {
			continue
		}
		if km := haversineKm(p[1], p[0], w.pubLat, w.pubLng); km < 1 {
			return fmt.Errorf("a vertex %.2f km from the place was drawn", km)
		}
	}
	return nil
}
