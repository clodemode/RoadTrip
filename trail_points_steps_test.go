package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/trail_points_from_chester_and_plays.feature. The Messages
// API is the apiStub (nothing reaches Anthropic); the player reports a start
// the way the phone does, with the phone's position beside it.

type tpWorld struct {
	t    *testing.T
	app  *App
	kris *http.Cookie
	key  int64
}

func initTrailPointScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &tpWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = tpWorld{t: t, key: 1760000000000}
		return ctx, nil
	})
	sc.Step(`^the trip is active with Chester and RoadTracks on, and the driver is signed in$`, func() error {
		stub := newAPIStub(t, quickScript(), quickScript())
		w.app = chesterApp(t, stub)
		w.app.tracks = newRoadtracks(rtKey, newMO(t).srv.URL)
		w.kris = asUser(t, w.app, "kris", roleAdmin)
		return nil
	})
	sc.Step(`^the driver asks Chester with the phone at ([-0-9.]+), ([-0-9.]+)$`, func(lat, lng string) error {
		return w.ask(url.Values{"preset": {"fuel"}, "lat": {lat}, "lng": {lng}})
	})
	sc.Step(`^the driver asks Chester with the phone's position off$`, func() error {
		return w.ask(url.Values{"preset": {"fuel"}})
	})
	sc.Step(`^the driver's player starts a track with the phone at ([-0-9.]+), ([-0-9.]+)$`, func(lat, lng string) error {
		return w.play(url.Values{"lat": {lat}, "lng": {lng}})
	})
	sc.Step(`^the driver's player starts a track with no phone position$`, func() error { return w.play(url.Values{}) })
	sc.Step(`^the trail holds (\d+) points?$`, w.holds)
	sc.Step(`^trail point (\d+) is at ([-0-9.]+), ([-0-9.]+) from "([^"]*)"$`, w.pointIs)
	sc.Step(`^no stored trail point carries the number "([^"]*)"$`, w.noneCarries)
	sc.Step(`^the route's track passes ([-0-9.]+), ([-0-9.]+) and then ([-0-9.]+), ([-0-9.]+)$`, w.trackPasses)
}

func (w *tpWorld) ask(form url.Values) error {
	rec := doAs(w.t, w.app, w.kris, "POST", "/chester/ask", form)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"t":"error"`) {
		return fmt.Errorf("the question was not answered: %d %s", rec.Code, rec.Body.String())
	}
	return nil
}

func (w *tpWorld) play(extra url.Values) error {
	w.key++
	form := url.Values{"album_id": {"42"}, "album": {"Abbey Road"}, "artist": {"The Beatles"}, "track_idx": {"0"}, "position_s": {"0.4"},
		"title": {"Come Together"}, "year": {"1969"}, "started": {strconv.FormatInt(w.key, 10)}}
	for k, v := range extra {
		form[k] = v
	}
	rec := doAs(w.t, w.app, w.kris, "POST", "/driver/roadtracks/state", form)
	if rec.Code != http.StatusNoContent {
		return fmt.Errorf("the start was refused: %d %s", rec.Code, rec.Body.String())
	}
	return nil
}

func (w *tpWorld) points() ([]TrailPoint, error) {
	return w.app.store.TrailPoints(w.app.activeSlug())
}

func (w *tpWorld) holds(n int) error {
	pts, err := w.points()
	if err != nil {
		return err
	}
	if len(pts) != n {
		return fmt.Errorf("the trail holds %d point(s), want %d: %+v", len(pts), n, pts)
	}
	return nil
}

func (w *tpWorld) pointIs(i int, lat, lng float64, source string) error {
	pts, err := w.points()
	if err != nil {
		return err
	}
	if i < 1 || i > len(pts) {
		return fmt.Errorf("there is no trail point %d (%d held)", i, len(pts))
	}
	p := pts[i-1]
	if !sameDeg(p.Lat, lat) || !sameDeg(p.Lng, lng) || p.Source != source {
		return fmt.Errorf("trail point %d is %v, %v from %q, want %v, %v from %q", i, p.Lat, p.Lng, p.Source, lat, lng, source)
	}
	return nil
}

func (w *tpWorld) noneCarries(num string) error {
	rows, err := w.app.store.db.Query(`SELECT lat, lng FROM trail_points`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var la, ln float64
		if err := rows.Scan(&la, &ln); err != nil {
			return err
		}
		for _, v := range []float64{la, ln} {
			if strings.Contains(strconv.FormatFloat(v, 'f', -1, 64), strings.TrimPrefix(num, "-")) {
				return fmt.Errorf("a stored trail point carries %s (%v, %v)", num, la, ln)
			}
		}
	}
	return rows.Err()
}

func (w *tpWorld) trackPasses(la1, ln1, la2, ln2 float64) error {
	body := send(w.t, w.app, "GET", "/route.geojson", testToken, "", nil).Body.Bytes()
	var doc struct {
		Features []struct {
			Geometry struct {
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return err
	}
	for _, f := range doc.Features {
		if f.Properties["kind"] != "track" {
			continue
		}
		var coords [][]float64
		if json.Unmarshal(f.Geometry.Coordinates, &coords) != nil {
			continue
		}
		first, second := -1, -1
		for i, c := range coords {
			if len(c) < 2 {
				continue
			}
			if sameDeg(c[1], la1) && sameDeg(c[0], ln1) && first < 0 {
				first = i
			}
			if sameDeg(c[1], la2) && sameDeg(c[0], ln2) && second < 0 {
				second = i
			}
		}
		if first >= 0 && second > first {
			return nil
		}
	}
	return fmt.Errorf("no track passes %v, %v and then %v, %v: %s", la1, ln1, la2, ln2, string(body))
}
