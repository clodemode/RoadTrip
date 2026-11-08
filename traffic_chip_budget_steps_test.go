package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/traffic_chip_budget.feature. TomTom and the National
// Weather Service are stubbed at the HTTP boundary with synthetic roads; the
// leg is a straight synthetic line east along a parallel.

const tcbLat, tcbLng0 = 44.0, -80.0

type tcbIncident struct {
	id, desc, from, to string
	cat, mag           int
	geom               string // "" for none
}

type tcbWorld struct {
	t       *testing.T
	line    json.RawMessage
	incs    []tcbIncident
	fail    bool
	carMile *float64
	chips   []hazardChip
	svc     *hazardSvc

	tt   *httptest.Server
	nws  *httptest.Server
	mu   sync.Mutex
	reqs []string // raw TomTom queries
	mark int
}

// tcbLngAt is the longitude of a point the given miles east of the start.
func tcbLngAt(mi float64) float64 {
	kmPerDeg := 111.195 * math.Cos(tcbLat*math.Pi/180)
	return tcbLng0 + mi/kmToMi/kmPerDeg
}

func (w *tcbWorld) point(mi float64) string {
	return fmt.Sprintf(`{"type":"Point","coordinates":[%.6f,%.6f]}`, tcbLngAt(mi), tcbLat)
}

func initTrafficChipBudgetScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &tcbWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = tcbWorld{t: t}
		w.tt = httptest.NewServer(http.HandlerFunc(w.serveTT))
		w.nws = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { rw.Write([]byte(`{"features":[]}`)) }))
		w.svc = newHazardSvc(ttKey)
		w.svc.timeout = 500 * time.Millisecond
		w.svc.ttBase, w.svc.nwsBase = w.tt.URL, w.nws.URL
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.tt.Close()
		w.nws.Close()
		return ctx, nil
	})
	sc.Step(`^a leg (\d+) miles long running east$`, func(n int) error {
		var b []string
		for mi := 0; mi <= n; mi += 10 {
			b = append(b, fmt.Sprintf("[%.6f,%.6f]", tcbLngAt(float64(mi)), tcbLat))
		}
		w.line = json.RawMessage("[" + strings.Join(b, ",") + "]")
		return nil
	})
	add := func(kind, text string, geom string) {
		parts := strings.SplitN(text, " to ", 2)
		in := tcbIncident{id: fmt.Sprintf("inc-%d", len(w.incs)+1), desc: "Road closed", cat: 8, mag: 4, geom: geom}
		if kind == "delay" {
			in.desc, in.cat, in.mag = "Stationary traffic", 1, 3
		}
		in.from = parts[0]
		if len(parts) == 2 {
			in.to = parts[1]
		}
		w.incs = append(w.incs, in)
	}
	sc.Step(`^TomTom reports (\d+) closures far from the leg$`, func(n int) error {
		for i := 1; i <= n; i++ {
			add("closure", fmt.Sprintf("Side Rd %d to Lane %d", i, i), fmt.Sprintf(`{"type":"Point","coordinates":[%.6f,%.6f]}`, tcbLngAt(float64(i)), tcbLat+0.5))
		}
		return nil
	})
	sc.Step(`^TomTom reports a (closure|delay) "([^"]*)" at mile (\d+)$`, func(kind, text string, mi int) error {
		add(kind, text, w.point(float64(mi)))
		return nil
	})
	sc.Step(`^TomTom reports a (closure|delay) "([^"]*)" from mile (\d+) to mile (\d+)$`, func(kind, text string, a, b int) error {
		var cs []string
		for mi := a; mi <= b; mi += 5 {
			cs = append(cs, fmt.Sprintf("[%.6f,%.6f]", tcbLngAt(float64(mi)), tcbLat))
		}
		add(kind, text, `{"type":"LineString","coordinates":[`+strings.Join(cs, ",")+`]}`)
		return nil
	})
	sc.Step(`^TomTom reports a (closure|delay) "([^"]*)" with no geometry$`, func(kind, text string) error {
		add(kind, text, "")
		return nil
	})
	sc.Step(`^TomTom answers 500$`, func() error { w.fail = true; return nil })
	sc.Step(`^the car is at mile (\d+)$`, func(mi int) error { f := float64(mi); w.carMile = &f; return nil })
	sc.Step(`^the car has no position$`, func() error { w.carMile = nil; return nil })
	sc.Step(`^the Trip hazards are asked for$`, func() error { w.askTrip(); return nil })
	sc.Step(`^the car moves to mile (\d+) and the Trip hazards are asked for again$`, func(mi int) error {
		w.mu.Lock()
		w.mark = len(w.reqs)
		w.mu.Unlock()
		f := float64(mi)
		w.carMile = &f
		w.askTrip()
		return nil
	})
	sc.Step(`^the hazards are asked for without a leg line$`, func() error {
		w.chips = w.svc.ForLeg(context.Background(), "free:test", legSamples(w.line, 4, nil))
		return nil
	})
	sc.Step(`^the traffic chips read:$`, func(doc *godog.DocString) error {
		var tr []hazardChip
		for _, c := range w.chips {
			if c.Source == "Traffic" {
				tr = append(tr, c)
			}
		}
		if got, want := chipTexts(tr), strings.TrimSpace(doc.Content); got != want {
			return fmt.Errorf("traffic chips differ\nwant:\n%s\ngot:\n%s", want, got)
		}
		return nil
	})
	sc.Step(`^TomTom was asked for geometry$`, func() error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if len(w.reqs) == 0 {
			return fmt.Errorf("TomTom was not asked at all")
		}
		for _, q := range w.reqs {
			if !strings.Contains(q, "geometry") {
				return fmt.Errorf("a request did not ask for geometry: %s", q)
			}
		}
		return nil
	})
	sc.Step(`^TomTom was not asked for geometry$`, func() error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if len(w.reqs) == 0 {
			return fmt.Errorf("TomTom was not asked at all")
		}
		for _, q := range w.reqs {
			if strings.Contains(q, "geometry") {
				return fmt.Errorf("a request asked for geometry: %s", q)
			}
		}
		return nil
	})
	sc.Step(`^TomTom was not asked again$`, func() error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if n := len(w.reqs) - w.mark; n != 0 {
			return fmt.Errorf("TomTom was asked %d more time(s) inside the cache window", n)
		}
		return nil
	})
	sc.Step(`^the TomTom key appears in no chip$`, func() error {
		if strings.Contains(chipTexts(w.chips), ttKey) {
			return fmt.Errorf("the key is in a chip: %s", chipTexts(w.chips))
		}
		return nil
	})
}

func (w *tcbWorld) askTrip() {
	var ahead *float64
	if w.carMile != nil {
		if al, _, _, ok := alongLine(w.line, tcbLat, tcbLngAt(*w.carMile)); ok {
			ahead = &al
		}
	}
	w.chips = w.svc.ForTrip(context.Background(), "leg-test", legSamples(w.line, 4, nil), w.line, ahead)
}

func (w *tcbWorld) serveTT(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	w.reqs = append(w.reqs, r.URL.RawQuery)
	w.mu.Unlock()
	if w.fail {
		http.Error(rw, "key="+ttKey+" boom", 500)
		return
	}
	var items []string
	for _, in := range w.incs {
		g := ""
		if in.geom != "" {
			g = `,"geometry":` + in.geom
		}
		items = append(items, fmt.Sprintf(`{"type":"Feature"%s,"properties":{"id":%q,"iconCategory":%d,"magnitudeOfDelay":%d,"events":[{"description":%q}],"from":%q,"to":%q}}`,
			g, in.id, in.cat, in.mag, in.desc, in.from, in.to))
	}
	rw.Write([]byte(`{"incidents":[` + strings.Join(items, ",") + `]}`))
}
