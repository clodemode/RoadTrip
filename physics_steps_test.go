package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/physics_of_travel.feature. They ride the start-and-check-in
// world (four invented stops 55 km apart on one meridian, a signed-in kris, a
// fake bus) and add: the car's last check-in at a clock time on the synthetic
// 7 November, photos with a time and no place or a time in 2099, a phone fix
// with an accuracy, and readers for a post's flag and time. Nothing here
// calls a real service.
type physWorld struct {
	t    *testing.T
	s    *scWorld
	snap map[int64][2]string // each stop's arrival and skip stamps, taken when the last check-in was set
}

func initPhysicsScenarios(t *testing.T, sc *godog.ScenarioContext, s *scWorld) {
	w := &physWorld{t: t, s: s}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = physWorld{t: t, s: s}
		return ctx, nil
	})
	sc.Step(`^the car was last checked in at ([ABCD]) at (\d\d:\d\d)$`, w.lastCheckin)
	sc.Step(`^a photo taken at (\d\d:\d\d) with no GPS of its own$`, func(hm string) error {
		s.p.photo = jpegTaken("2026:11:07 " + hm + ":00")
		return nil
	})
	sc.Step(`^a photo taken in the year 2099 with its GPS at ([ABCD])$`, func(which string) error {
		i := strings.Index("ABCD", which)
		s.p.photo = jpegGPSAt("2099:01:01 10:00:00", s.p.cps[i].Lat, s.p.cps[i].Lng)
		return nil
	})
	sc.Step(`^the phone reports a position at B with an accuracy of (\d+) metres$`, func(m string) error {
		s.phone, s.phoneAcc = true, m
		return nil
	})
	sc.Step(`^no stop is checked in$`, w.nothingChanged)
	sc.Step(`^the post is flagged as out of step$`, func() error { return w.flag("out_of_step", true) })
	sc.Step(`^the post is not flagged$`, func() error { return w.noFlag() })
	sc.Step(`^the post says its time came from the phone$`, func() error { return w.flag("time_from_phone", true) })
	sc.Step(`^the notice says the post was kept as out of step$`, func() error { return s.noticeHas("out of step") })
	sc.Step(`^([ABCD]) is still arrived at (\d\d:\d\d) in [ABCD]'s zone$`, func(which, hm string) error {
		return s.arrivedAt(strings.Index("ABCD", which), hm)
	})
	sc.Step(`^the post exists at (\d\d:\d\d) in its own zone$`, w.postAt)
	sc.Step(`^the post exists at the phone's time$`, w.postNow)
	sc.Step(`^([ABCD]) is arrived at the phone's time$`, w.arrivedNow)
	sc.Step(`^a leg of 165 km whose plan says it takes six minutes$`, w.shortPlan)
	sc.Step(`^the HUD is asked moving at its start$`, w.hudAtStart)
	sc.Step(`^the ETA written to the ledger is at least (\d+) minutes ahead$`, w.etaAhead)
}

// lastCheckin makes stop i the car's latest arrival at a wall-clock time in its
// own zone on the synthetic day. The drive is started at A the day before, so
// the stop is a check-in and not the start.
func (w *physWorld) lastCheckin(which, hm string) error {
	st := w.s.p.sim.app.store
	i := strings.Index("ABCD", which)
	if i != 0 {
		if err := st.SetArrived(w.s.p.cps[0].ID, time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC), nil); err != nil {
			return err
		}
	}
	var h, m int
	if _, err := fmt.Sscanf(hm, "%d:%d", &h, &m); err != nil {
		return err
	}
	at := time.Date(2026, 11, 7, h, m, 0, 0, loadLoc(w.s.p.cps[i].TZ))
	if err := st.SetArrived(w.s.p.cps[i].ID, at, nil); err != nil {
		return err
	}
	w.snap = map[int64][2]string{}
	for j := range w.s.p.cps {
		c := w.s.p.cp(j)
		w.snap[c.ID] = [2]string{c.ArrivedAt, c.SkippedAt}
	}
	return nil
}

// nothingChanged: no stop has gained an arrival or a skip since the last
// check-in was set.
func (w *physWorld) nothingChanged() error {
	for j := range w.s.p.cps {
		c := w.s.p.cp(j)
		if got := [2]string{c.ArrivedAt, c.SkippedAt}; got != w.snap[c.ID] {
			return fmt.Errorf("%s changed: arrived/skipped %q, was %q (answer %d %s)", c.Name, got, w.snap[c.ID], w.s.p.last.Code, w.s.p.last.Header().Get("Location"))
		}
	}
	return nil
}

func (w *physWorld) thePost() (*Post, error) {
	posts, err := w.s.p.sim.app.store.Posts("post-photo-trip", 0)
	if err != nil {
		return nil, err
	}
	if len(posts) != 1 {
		return nil, fmt.Errorf("want one post, have %d", len(posts))
	}
	return &posts[0], nil
}

func (w *physWorld) flag(word string, want bool) error {
	p, err := w.thePost()
	if err != nil {
		return err
	}
	if has := strings.Contains(" "+p.Flag+" ", " "+word+" "); has != want {
		return fmt.Errorf("the post's flag is %q, want it to carry %q", p.Flag, word)
	}
	return nil
}

func (w *physWorld) noFlag() error {
	p, err := w.thePost()
	if err != nil {
		return err
	}
	if p.Flag != "" {
		return fmt.Errorf("the post is flagged %q", p.Flag)
	}
	return nil
}

func (w *physWorld) postAt(hm string) error {
	p, err := w.thePost()
	if err != nil {
		return err
	}
	at, ok := parseUTC(p.OccurredAt)
	if !ok {
		return fmt.Errorf("the post's time %q does not read", p.OccurredAt)
	}
	if got := at.In(loadLoc(p.TZ)).Format("2006-01-02 15:04"); got != "2026-11-07 "+hm {
		return fmt.Errorf("the post is at %s in %s, want 2026-11-07 %s", got, p.TZ, hm)
	}
	return nil
}

func nearNow(s string) error {
	at, ok := parseUTC(s)
	if !ok {
		return fmt.Errorf("%q does not read as a time", s)
	}
	if d := time.Since(at); d < -2*time.Minute || d > 5*time.Minute {
		return fmt.Errorf("%s is %s from now, want within a few minutes", s, d.Round(time.Second))
	}
	return nil
}

func (w *physWorld) postNow() error {
	p, err := w.thePost()
	if err != nil {
		return err
	}
	return nearNow(p.OccurredAt)
}

func (w *physWorld) arrivedNow(which string) error {
	return nearNow(w.s.p.cp(strings.Index("ABCD", which)).ArrivedAt)
}

func (w *physWorld) shortPlan() error {
	_, err := w.s.p.sim.app.store.db.Exec(`UPDATE legs SET est_hours=0.1 WHERE id=?`, w.s.p.cps[0].LegID)
	return err
}

func (w *physWorld) hudAtStart() error {
	st := w.s.p.cps[0]
	target := fmt.Sprintf("/driver/hud.json?lat=%.4f&lng=%.4f&moving=1", st.Lat, st.Lng)
	r := doAs(w.t, w.s.p.sim.app, w.s.p.sim.cookies["kris"], "GET", target, nil)
	if r.Code != http.StatusOK {
		return fmt.Errorf("hud.json answered %d: %s", r.Code, r.Body.String())
	}
	var h hudView
	return json.Unmarshal(r.Body.Bytes(), &h)
}

func (w *physWorld) etaAhead(min int) error {
	rows, err := w.s.p.sim.app.store.ETALedger("post-photo-trip")
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("no ETA was written to the ledger")
	}
	r := rows[0]
	if d := r.PredictedArrival.Sub(r.PredictedAt); d < time.Duration(min)*time.Minute {
		return fmt.Errorf("the ETA is %s ahead, want at least %d minutes", d.Round(time.Second), min)
	}
	return nil
}
