package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// etaWorld rides on the sim world: same app, signed-in cookies, fake Redis.
type etaWorld struct {
	t    *testing.T
	sim  *simWorld
	q    string // the query the next HUD ask carries
	hud  hudView
	pred time.Time
	rv   *rideView
}

func initEtaScenarios(t *testing.T, sc *godog.ScenarioContext, sim *simWorld) {
	e := &etaWorld{t: t, sim: sim}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*e = etaWorld{t: t, sim: sim}
		return ctx, nil
	})
	sc.Step(`^the HUD is asked with no position$`, func() error { return e.ask("") })
	sc.Step(`^a fix on the first leg that is not moving$`, func() error { return e.fixOnFirstLeg(false) })
	sc.Step(`^a moving fix on the first leg$`, func() error { return e.fixOnFirstLeg(true) })
	sc.Step(`^a moving fix (\d+) miles from every leg$`, e.fixFarFromEveryLeg)
	sc.Step(`^the HUD is asked with that fix$`, func() error { return e.ask(e.q) })
	sc.Step(`^the HUD is asked with that fix three times within one minute$`, func() error {
		for i := 0; i < 3; i++ {
			if err := e.ask(e.q); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the HUD shows no ETA$`, e.noETA)
	sc.Step(`^the HUD shows an ETA that begins with "([^"]*)"$`, e.etaBeginsWith)
	sc.Step(`^its minutes are a multiple of five$`, e.minutesMultipleOfFive)
	sc.Step(`^it is labelled plan pace$`, e.planPace)
	sc.Step(`^the HUD says it is off route and gives the distance to the plan in whole miles$`, e.offRoute)
	sc.Step(`^the HUD shows no ETA and no miles done$`, e.noETAnoMiles)
	sc.Step(`^exactly one ETA prediction is recorded for the next stop$`, e.oneRecorded)
	sc.Step(`^an ETA prediction for the next stop with predicted arrival T$`, e.seedPrediction)
	sc.Step(`^kris marks the next stop arrived at T plus (\d+) minutes$`, e.arriveLate)
	sc.Step(`^that prediction is scored with an error of (\d+) minutes$`, e.scored)
	sc.Step(`^the bus carries an eta_hit record for that stop with the error in minutes$`, e.busEtaHit)
	sc.Step(`^that record carries no latitude and no longitude$`, func() error { return sim.meltHasNoCoordinates() })
	sc.Step(`^a scored ETA prediction exists$`, func() error {
		if err := e.seedPrediction(); err != nil {
			return err
		}
		return e.arriveLate(12)
	})
	sc.Step(`^kris opens the ETA ledger$`, e.openLedger)
	sc.Step(`^it lists the prediction with its predicted time, actual time and error in minutes$`, e.ledgerLists)
	sc.Step(`^it states the share of predictions within ten minutes$`, e.ledgerShare)
	sc.Step(`^a Destined route whose arrival is (\d+:\d+ [AP]M)$`, e.destinedArrival)
	sc.Step(`^the Destined view shows "([^"]*)"$`, e.destinedShows)
}

func (e *etaWorld) ride() (*rideView, error) {
	if e.rv != nil {
		return e.rv, nil
	}
	rv, err := e.sim.app.loadRide()
	e.rv = rv
	return rv, err
}

func (e *etaWorld) ask(q string) error {
	target := "/driver/hud.json"
	if q != "" {
		target += "?" + q
	}
	w := doAs(e.t, e.sim.app, e.sim.cookies["kris"], "GET", target, nil)
	if w.Code != http.StatusOK {
		return fmt.Errorf("hud.json answered %d: %s", w.Code, w.Body.String())
	}
	e.hud = hudView{}
	return json.Unmarshal(w.Body.Bytes(), &e.hud)
}

func (e *etaWorld) fixOnFirstLeg(moving bool) error {
	rv, err := e.ride()
	if err != nil {
		return err
	}
	lat, lng := midVertex(e.t, rv.Legs[0])
	e.q = fmt.Sprintf("lat=%.4f&lng=%.4f", lat, lng)
	if moving {
		e.q += "&moving=1"
	}
	return nil
}

func (e *etaWorld) fixFarFromEveryLeg(miles int) error {
	rv, err := e.ride()
	if err != nil {
		return err
	}
	lat, lng := midVertex(e.t, rv.Legs[0])
	// Walk north until the point is further than the HUD believes from every
	// leg, starting from the asked distance.
	for deg := float64(miles) / 69; deg < 40; deg += 0.25 {
		la := lat + deg
		far := true
		for _, l := range rv.Legs {
			if _, _, off, ok := alongLine(l.Line, la, lng); ok && off < hudOffRouteKm {
				far = false
			}
		}
		if far {
			e.q = fmt.Sprintf("lat=%.4f&lng=%.4f&moving=1", la, lng)
			return nil
		}
	}
	return fmt.Errorf("no point is far from every leg")
}

func etaShown(h hudView) bool { return h.ETA != "" && h.ETA != "—" }

func (e *etaWorld) noETA() error {
	if etaShown(e.hud) {
		return fmt.Errorf("the HUD shows an ETA: %q", e.hud.ETA)
	}
	return nil
}

func (e *etaWorld) etaBeginsWith(prefix string) error {
	if !strings.HasPrefix(e.hud.ETA, prefix) {
		return fmt.Errorf("ETA is %q", e.hud.ETA)
	}
	return nil
}

var aboutRe = regexp.MustCompile(`^about (\d{1,2}):(\d{2}) [AP]M [A-Z]{2,5}$`)

func (e *etaWorld) minutesMultipleOfFive() error {
	m := aboutRe.FindStringSubmatch(e.hud.ETA)
	if m == nil {
		return fmt.Errorf("ETA %q is not of the form about H:MM AM ZONE", e.hud.ETA)
	}
	if n, _ := strconv.Atoi(m[2]); n%5 != 0 {
		return fmt.Errorf("ETA %q minutes %d", e.hud.ETA, n)
	}
	return nil
}

func (e *etaWorld) planPace() error {
	if e.hud.ETAPace != "plan pace" {
		return fmt.Errorf("pace label is %q", e.hud.ETAPace)
	}
	return nil
}

func (e *etaWorld) offRoute() error {
	if !e.hud.OffRoute || e.hud.OffRouteMiles < 31 {
		return fmt.Errorf("off route %v, %d mi", e.hud.OffRoute, e.hud.OffRouteMiles)
	}
	return nil
}

func (e *etaWorld) noETAnoMiles() error {
	if err := e.noETA(); err != nil {
		return err
	}
	if e.hud.HaveMiles || e.hud.MilesDone != "—" || e.hud.NextMiles != "—" {
		return fmt.Errorf("miles shown: %+v", e.hud)
	}
	return nil
}

// stop is the checkpoint the ETA is for: the end of the first leg.
func (e *etaWorld) stop() (*Checkpoint, string, error) {
	rv, err := e.ride()
	if err != nil {
		return nil, "", err
	}
	cps := rv.Legs[0].Checkpoints
	return &cps[len(cps)-1], rv.Ride.Slug, nil
}

func (e *etaWorld) oneRecorded() error {
	cp, ride, err := e.stop()
	if err != nil {
		return err
	}
	rows, err := e.sim.app.store.ETALedger(ride)
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0].CheckpointID != cp.ID {
		return fmt.Errorf("want one prediction for %q, ledger has %+v", cp.Name, rows)
	}
	return nil
}

func (e *etaWorld) seedPrediction() error {
	cp, ride, err := e.stop()
	if err != nil {
		return err
	}
	e.pred = time.Now().Add(-2 * time.Hour).Truncate(time.Minute)
	_, err = e.sim.app.store.RecordETA(ride, cp.ID, "kris", e.pred.Add(-time.Hour), e.pred, "plan")
	return err
}

func (e *etaWorld) arriveLate(min int) error {
	cp, _, err := e.stop()
	if err != nil {
		return err
	}
	at := e.pred.Add(time.Duration(min) * time.Minute).Format(time.RFC3339)
	w := doAs(e.t, e.sim.app, e.sim.cookies["kris"], "POST", "/driver/checkpoint/"+strconv.FormatInt(cp.ID, 10)+"/arrive",
		url.Values{"at": {at}})
	if w.Code != http.StatusSeeOther {
		return fmt.Errorf("arrive answered %d: %s", w.Code, w.Body.String())
	}
	return nil
}

func (e *etaWorld) scored(min int) error {
	_, ride, _ := e.stop()
	rows, err := e.sim.app.store.ETALedger(ride)
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0].ErrorMin == nil || *rows[0].ErrorMin != min {
		return fmt.Errorf("ledger: %+v", rows)
	}
	return nil
}

func (e *etaWorld) busEtaHit() error {
	cp, _, _ := e.stop()
	f := e.sim.redis
	for i := 0; i < 200; i++ {
		f.mu.Lock()
		cmds := append([][]string(nil), f.cmds...)
		f.mu.Unlock()
		for _, c := range cmds {
			if len(c) < 3 || c[0] != "LPUSH" {
				continue
			}
			var rec map[string]any
			if json.Unmarshal([]byte(c[2]), &rec) != nil || rec["event"] != "eta_hit" {
				continue
			}
			e.sim.hudRec = rec
			if rec["checkpoint"] != cp.Name {
				return fmt.Errorf("eta_hit names %v, want %q", rec["checkpoint"], cp.Name)
			}
			if rec["error_min"] != float64(12) || rec["n"] != float64(1) {
				return fmt.Errorf("eta_hit fields: %v", rec)
			}
			if _, ok := rec["mean_abs_error_min"]; !ok {
				return fmt.Errorf("eta_hit has no mean_abs_error_min: %v", rec)
			}
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("no eta_hit record reached the bus")
}

type ledgerOut struct {
	Predictions []struct {
		Checkpoint       string `json:"checkpoint"`
		PredictedArrival string `json:"predicted_arrival"`
		ActualArrival    string `json:"actual_arrival"`
		ErrorMin         *int   `json:"error_min"`
	} `json:"predictions"`
	HitRate10 *float64 `json:"hit_rate_10"`
}

func (e *etaWorld) openLedger() error {
	w := doAs(e.t, e.sim.app, e.sim.cookies["kris"], "GET", "/driver/eta.json", nil)
	if w.Code != http.StatusOK {
		return fmt.Errorf("eta.json answered %d", w.Code)
	}
	e.sim.last = w
	return nil
}

func (e *etaWorld) ledger() (ledgerOut, error) {
	var l ledgerOut
	err := json.Unmarshal(e.sim.last.Body.Bytes(), &l)
	return l, err
}

func (e *etaWorld) ledgerLists() error {
	l, err := e.ledger()
	if err != nil {
		return err
	}
	if len(l.Predictions) != 1 {
		return fmt.Errorf("ledger: %s", e.sim.last.Body.String())
	}
	p := l.Predictions[0]
	if p.Checkpoint == "" || p.PredictedArrival == "" || p.ActualArrival == "" || p.ErrorMin == nil || *p.ErrorMin != 12 {
		return fmt.Errorf("row: %+v", p)
	}
	return nil
}

func (e *etaWorld) ledgerShare() error {
	l, err := e.ledger()
	if err != nil {
		return err
	}
	if l.HitRate10 == nil || *l.HitRate10 != 0 {
		return fmt.Errorf("hit_rate_10 for a 12 minute miss: %v", l.HitRate10)
	}
	return nil
}

func (e *etaWorld) destinedArrival(clock string) error {
	tm, err := time.Parse("3:04 PM", clock)
	if err != nil {
		return err
	}
	tt := newTT(e.t)
	tt.routeBody = strings.Replace(routeOK, "15:40:00-06:00", tm.Format("15:04")+":00-06:00", 1)
	app := e.sim.app
	app.free = newFreeSvc(ttKey)
	app.free.ttBase = tt.srv.URL
	nws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"features":[]}`)) }))
	e.t.Cleanup(nws.Close)
	app.hazards = hazardSvcFor(nws, tt.srv, ttKey)
	goDestined(e.t, app, e.sim.cookies["kris"], "hudson bend")
	w := doAs(e.t, app, e.sim.cookies["kris"], "GET", "/driver/hud.json?lat=30.2000&lng=-97.8000", nil)
	e.hud = hudView{}
	if err := json.Unmarshal(w.Body.Bytes(), &e.hud); err != nil {
		return err
	}
	return nil
}

func (e *etaWorld) destinedShows(want string) error {
	if e.hud.Free == nil || !strings.Contains(e.hud.Free.ETA, want) {
		return fmt.Errorf("Destined view ETA is %+v", e.hud.Free)
	}
	return nil
}
