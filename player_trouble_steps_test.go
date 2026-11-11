package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/player_trouble_is_recorded.feature. The trouble record is
// the MELT file sink in a throwaway folder; it is read back only through the
// signed-in route GET /driver/melt.json, the way a person would read it.

type ptWorld struct {
	t    *testing.T
	app  *App
	kris *http.Cookie
	code int
	off  bool
}

func initPlayerTroubleScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &ptWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = ptWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^the trouble record is kept in a file$`, func() error {
		w.app = activeTripApp(w.t)
		w.app.melt = newMeltSinks("", "test", filepath.Join(w.t.TempDir(), "melt", "roadtrip.jsonl"))
		return nil
	})
	sc.Step(`^the trouble record is not kept$`, func() error { w.app.melt = nil; return nil })
	sc.Step(`^the trip-keeper is signed in$`, func() error { w.kris = asUser(w.t, w.app, "kris", roleAdmin); return nil })
	sc.Step(`^the phone reports "([^"]*)" with ms "([^"]*)", code "([^"]*)", album (-?\d+) and track (\d+)$`, w.report)
	sc.Step(`^the phone reports "([^"]*)" with a lat and a lng field$`, func(ev string) error {
		return w.post(url.Values{"event": {ev}, "lat": {"44.23"}, "lng": {"-76.48"}})
	})
	sc.Step(`^the phone reports the unknown event "([^"]*)"$`, func(ev string) error { return w.post(url.Values{"event": {ev}}) })
	sc.Step(`^the phone is answered with no content$`, func() error { return w.wantCode(http.StatusNoContent) })
	sc.Step(`^the phone is answered with a bad request$`, func() error { return w.wantCode(http.StatusBadRequest) })
	sc.Step(`^the trouble record holds an? (events|errors) record "([^"]*)" from the phone with ms (\d+), code "([^"]*)", album (\d+) and track (\d+)$`, w.holds)
	sc.Step(`^that record holds nothing else from the request$`, w.nothingElse)
	sc.Step(`^the trouble record holds an errors record "([^"]*)" from the phone with no lat and no lng$`, func(ev string) error {
		r, err := w.one(ev)
		if err != nil {
			return err
		}
		if _, ok := r["lat"]; ok {
			return fmt.Errorf("the record kept lat: %v", r)
		}
		if _, ok := r["lng"]; ok {
			return fmt.Errorf("the record kept lng: %v", r)
		}
		return nil
	})
	sc.Step(`^a server record carrying a lat field is never written$`, func() error {
		w.app.melt.Error("guard_probe", map[string]any{"lat": 44.2})
		w.app.melt.Event("guard_after", map[string]any{"n": 1})
		if _, err := w.one("guard_after"); err != nil {
			return err
		}
		if recs := w.read("guard_probe"); len(recs) != 0 {
			return fmt.Errorf("a record carrying lat was written: %v", recs)
		}
		return nil
	})
	sc.Step(`^the trouble record holds an errors record "([^"]*)" from the phone with no ms, no code, no album and no track$`, func(ev string) error {
		r, err := w.one(ev)
		if err != nil {
			return err
		}
		for _, k := range []string{"ms", "code", "album_id", "track_idx"} {
			if _, ok := r[k]; ok {
				return fmt.Errorf("the record kept %s: %v", k, r)
			}
		}
		return nil
	})
	sc.Step(`^a signed-out caller cannot read the trouble record or report to it$`, func() error {
		for _, c := range []struct{ m, u string }{{"GET", "/driver/melt.json"}, {"POST", "/driver/roadtracks/event"}} {
			rec := doAs(w.t, w.app, nil, c.m, c.u, url.Values{"event": {"first_sound"}})
			if rec.Code == http.StatusOK || rec.Code == http.StatusNoContent {
				return fmt.Errorf("signed out %s %s answered %d", c.m, c.u, rec.Code)
			}
		}
		return nil
	})
	sc.Step(`^the trouble record answers that its sink is off with no records$`, func() error {
		rec := doAs(w.t, w.app, w.kris, "GET", "/driver/melt.json", nil)
		var doc struct {
			Sink    string           `json:"sink"`
			Records []map[string]any `json:"records"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc.Sink != "off" || doc.Records == nil || len(doc.Records) != 0 {
			return fmt.Errorf("answered %d %s", rec.Code, rec.Body.String())
		}
		return nil
	})
}

func (w *ptWorld) post(form url.Values) error {
	rec := doAs(w.t, w.app, w.kris, "POST", "/driver/roadtracks/event", form)
	w.code = rec.Code
	return nil
}

func (w *ptWorld) report(ev, ms, code, album, track string) error {
	return w.post(url.Values{"event": {ev}, "ms": {ms}, "code": {code}, "album_id": {album}, "track_idx": {track}})
}

func (w *ptWorld) wantCode(c int) error {
	if w.code != c {
		return fmt.Errorf("answered %d, want %d", w.code, c)
	}
	return nil
}

func (w *ptWorld) read(event string) []map[string]any {
	rec := doAs(w.t, w.app, w.kris, "GET", "/driver/melt.json?stream=all&n=200&event="+url.QueryEscape(event), nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" {
		return nil
	}
	var doc struct {
		Records []map[string]any `json:"records"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	return doc.Records
}

// one waits briefly for the background writer, then returns the newest record.
func (w *ptWorld) one(event string) (map[string]any, error) {
	for i := 0; i < 100; i++ {
		if recs := w.read(event); len(recs) > 0 {
			return recs[0], nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, fmt.Errorf("no %q record could be read from /driver/melt.json (status %d earlier)", event, w.code)
}

func (w *ptWorld) holds(stream, ev string, ms int, code string, album, track int) error {
	r, err := w.one(ev)
	if err != nil {
		return err
	}
	if r["stream"] != stream || r["origin"] != "phone" || r["ms"] != float64(ms) || r["album_id"] != float64(album) || r["track_idx"] != float64(track) {
		return fmt.Errorf("the record is %v", r)
	}
	if code == "" {
		if _, ok := r["code"]; ok {
			return fmt.Errorf("the record holds a code: %v", r)
		}
	} else if r["code"] != code {
		return fmt.Errorf("the record's code is %v", r["code"])
	}
	return nil
}

func (w *ptWorld) nothingElse() error {
	r, err := w.one("play_timeout")
	if err != nil {
		return err
	}
	allowed := map[string]bool{"ts": true, "source": true, "event": true, "origin": true, "venue": true, "stream": true,
		"ms": true, "code": true, "album_id": true, "track_idx": true}
	for k := range r {
		if !allowed[k] {
			return fmt.Errorf("the record holds %s=%v", k, r[k])
		}
	}
	return nil
}
