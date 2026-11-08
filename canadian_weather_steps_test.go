package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/canadian_weather.feature. Both weather sources are
// stubbed at the HTTP boundary, with synthetic alert text: nothing here
// reaches api.weather.gov or api.weather.gc.ca. The Environment Canada stub
// answers a request with the alerts registered for whichever sample point
// sits inside the requested bbox, in the GeoMet GeoJSON shape.

type cwAlert struct {
	typ, name, area, status string
	expires                 time.Time
}

type cwPoint struct {
	pt     ll
	nws    string // "empty" (default), "oob", "500", "400other" or "alerts"
	nwsTxt string
	gc     []cwAlert
	gcFail bool
}

type cwWorld struct {
	t      *testing.T
	pts    []*cwPoint
	chips  []hazardChip
	nws    *httptest.Server
	gc     *httptest.Server
	mu     sync.Mutex
	gcReqs []*http.Request
}

func initCanadianWeatherScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &cwWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = cwWorld{t: t}
		w.nws = httptest.NewServer(http.HandlerFunc(w.serveNWS))
		w.gc = httptest.NewServer(http.HandlerFunc(w.serveGC))
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.nws.Close()
		w.gc.Close()
		return ctx, nil
	})
	cur := func() (*cwPoint, error) {
		if len(w.pts) == 0 {
			return nil, fmt.Errorf("no sample point declared")
		}
		return w.pts[len(w.pts)-1], nil
	}
	sc.Step(`^a sample point at ([\d.-]+),([\d.-]+)$`, func(lat, lng string) error {
		la, _ := strconv.ParseFloat(lat, 64)
		lo, _ := strconv.ParseFloat(lng, 64)
		w.pts = append(w.pts, &cwPoint{pt: ll{la, lo}, nws: "empty"})
		return nil
	})
	sc.Step(`^the US source calls that point out of bounds$`, func() error {
		p, err := cur()
		if err == nil {
			p.nws = "oob"
		}
		return err
	})
	sc.Step(`^the US source answers 500 at that point$`, func() error {
		p, err := cur()
		if err == nil {
			p.nws = "500"
		}
		return err
	})
	sc.Step(`^the US source answers 400 about something else at that point$`, func() error {
		p, err := cur()
		if err == nil {
			p.nws = "400other"
		}
		return err
	})
	sc.Step(`^the US source has an active "([^"]*)" for "([^"]*)" at that point$`, func(ev, area string) error {
		p, err := cur()
		if err == nil {
			p.nws = "alerts"
			p.nwsTxt = fmt.Sprintf(`{"features":[{"properties":{"id":"us-%s","event":%q,"severity":"Severe","areaDesc":%q}}]}`, strings.ReplaceAll(ev, " ", "-"), ev, area)
		}
		return err
	})
	sc.Step(`^Environment Canada has an active (warning|watch|advisory|statement) "([^"]*)" for "([^"]*)" at that point$`, func(typ, name, area string) error {
		p, err := cur()
		if err == nil {
			p.gc = append(p.gc, cwAlert{typ, name, area, "issued", time.Now().Add(6 * time.Hour)})
		}
		return err
	})
	sc.Step(`^Environment Canada has a cancelled (warning) "([^"]*)" for "([^"]*)" at that point$`, func(typ, name, area string) error {
		p, err := cur()
		if err == nil {
			p.gc = append(p.gc, cwAlert{typ, name, area, "ended", time.Now().Add(6 * time.Hour)})
		}
		return err
	})
	sc.Step(`^Environment Canada has an expired (warning) "([^"]*)" for "([^"]*)" at that point$`, func(typ, name, area string) error {
		p, err := cur()
		if err == nil {
			p.gc = append(p.gc, cwAlert{typ, name, area, "continued", time.Now().Add(-2 * time.Hour)})
		}
		return err
	})
	sc.Step(`^Environment Canada fails at that point$`, func() error {
		p, err := cur()
		if err == nil {
			p.gcFail = true
		}
		return err
	})
	sc.Step(`^the weather chips are asked for$`, func() error {
		s := newHazardSvc("")
		s.timeout = 500 * time.Millisecond
		s.nwsBase = w.nws.URL
		s.gcBase = w.gc.URL
		var pts []ll
		for _, p := range w.pts {
			pts = append(pts, p.pt)
		}
		w.chips = s.Weather(context.Background(), pts)
		return nil
	})
	sc.Step(`^the chips read:$`, func(doc *godog.DocString) error {
		want := strings.TrimSpace(doc.Content)
		if got := chipTexts(w.chips); got != want {
			return fmt.Errorf("chips differ\nwant:\n%s\ngot:\n%s", want, got)
		}
		return nil
	})
	sc.Step(`^Environment Canada was not asked$`, func() error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if len(w.gcReqs) != 0 {
			return fmt.Errorf("Environment Canada was asked %d time(s)", len(w.gcReqs))
		}
		return nil
	})
	sc.Step(`^Environment Canada was asked once, with the project's public identity and a small box around the point$`, func() error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if len(w.gcReqs) != 1 {
			return fmt.Errorf("Environment Canada was asked %d time(s), want 1", len(w.gcReqs))
		}
		r := w.gcReqs[0]
		if ua := r.Header.Get("User-Agent"); ua != nwsUserAgent {
			return fmt.Errorf("User-Agent %q, want the project's %q", ua, nwsUserAgent)
		}
		var canadian *cwPoint
		for _, p := range w.pts {
			if p.nws == "oob" {
				canadian = p
			}
		}
		box, ok := parseBBox(r.URL.Query().Get("bbox"))
		if !ok || canadian == nil {
			return fmt.Errorf("no usable bbox in %q", r.URL.RawQuery)
		}
		if !(box[0] < canadian.pt.Lng && canadian.pt.Lng < box[2] && box[1] < canadian.pt.Lat && canadian.pt.Lat < box[3]) {
			return fmt.Errorf("bbox %v does not surround the point", box)
		}
		if box[2]-box[0] > 0.1 || box[3]-box[1] > 0.1 {
			return fmt.Errorf("bbox %v is not small", box)
		}
		return nil
	})
}

func parseBBox(s string) ([4]float64, bool) {
	var out [4]float64
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return out, false
	}
	for i, p := range parts {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return out, false
		}
		out[i] = f
	}
	return out, true
}

func (w *cwWorld) serveNWS(rw http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Query().Get("point"), ",")
	if len(parts) != 2 {
		http.Error(rw, "bad", 400)
		return
	}
	la, _ := strconv.ParseFloat(parts[0], 64)
	lo, _ := strconv.ParseFloat(parts[1], 64)
	for _, p := range w.pts {
		if cwNear(p.pt.Lat, la) && cwNear(p.pt.Lng, lo) {
			switch p.nws {
			case "oob":
				rw.WriteHeader(400)
				rw.Write([]byte(`{"title":"Bad Request","status":400,"detail":"Parameter \"point\" is invalid: out of bounds"}`))
			case "500":
				http.Error(rw, "boom", 500)
			case "400other":
				rw.WriteHeader(400)
				rw.Write([]byte(`{"title":"Bad Request","status":400,"detail":"Parameter \"point\" is invalid: bad format"}`))
			case "alerts":
				rw.Write([]byte(p.nwsTxt))
			default:
				rw.Write([]byte(`{"features":[]}`))
			}
			return
		}
	}
	rw.Write([]byte(`{"features":[]}`))
}

func cwNear(a, b float64) bool { d := a - b; return d < 0.0001 && d > -0.0001 }

func (w *cwWorld) serveGC(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	w.gcReqs = append(w.gcReqs, r)
	w.mu.Unlock()
	box, _ := parseBBox(url.Values(r.URL.Query()).Get("bbox"))
	var feats []string
	for _, p := range w.pts {
		if !(box[0] < p.pt.Lng && p.pt.Lng < box[2] && box[1] < p.pt.Lat && p.pt.Lat < box[3]) {
			continue
		}
		if p.gcFail {
			http.Error(rw, "boom", 500)
			return
		}
		for i, a := range p.gc {
			feats = append(feats, fmt.Sprintf(`{"id":"gc-%d-%s","type":"Feature","properties":{"alert_type":%q,"alert_name_en":%q,"alert_name_fr":"nom synthetique","feature_name_en":%q,"status_en":%q,"expiration_datetime":%q,"province":"ON"}}`,
				i, strings.ReplaceAll(a.name, " ", "-"), a.typ, a.name, a.area, a.status, a.expires.UTC().Format("2006-01-02T15:04:05.000Z")))
		}
	}
	rw.Write([]byte(`{"type":"FeatureCollection","features":[` + strings.Join(feats, ",") + `]}`))
}
