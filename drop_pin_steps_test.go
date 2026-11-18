package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// Steps for features/drop_pin_from_the_header.feature, on the postWorld of
// post_steps_test.go and the stops A..D of start_checkin_steps_test.go.

type dropPinWorld struct {
	p      *postWorld
	page   string
	last   *httptest.ResponseRecorder
	before int // posts on the ride before the pin
}

var dropPinBtnRe = regexp.MustCompile(`id="drop-pin"`)

func initDropPinScenarios(sc *godog.ScenarioContext, p *postWorld) {
	w := &dropPinWorld{p: p}
	sc.Step(`^kris opens the page "([^"]*)"$`, func(path string) error { return w.open(p.sim.cookies["kris"], path) })
	sc.Step(`^a visitor opens the Ride page for its header$`, func() error { return w.open(nil, "/") })
	sc.Step(`^the header carries exactly one Drop Pin button$`, func() error {
		if n := len(dropPinBtnRe.FindAllString(w.header(), -1)); n != 1 {
			return fmt.Errorf("the header carries %d Drop Pin buttons", n)
		}
		return nil
	})
	sc.Step(`^the header carries no Drop Pin button$`, func() error {
		if dropPinBtnRe.MatchString(w.page) {
			return fmt.Errorf("the visitor's page carries a Drop Pin button")
		}
		return nil
	})
	sc.Step(`^kris drops a pin with the phone at D$`, func() error { return w.drop(p.sim.cookies["kris"]) })
	sc.Step(`^a visitor drops a pin with the phone at D$`, func() error { return w.drop(nil) })
	sc.Step(`^the pin is accepted$`, func() error {
		if w.last.Code != http.StatusOK || !strings.Contains(w.last.Body.String(), `"dropped"`) {
			return fmt.Errorf("drop answered %d %s", w.last.Code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the pin is refused$`, func() error {
		if w.last.Code < 400 {
			return fmt.Errorf("a visitor's pin answered %d", w.last.Code)
		}
		return nil
	})
	sc.Step(`^no stop after A is arrived at or skipped$`, func() error {
		rv, err := p.sim.app.loadRide()
		if err != nil {
			return err
		}
		for _, c := range rideCheckpoints(rv.Legs) {
			if c.Ord > 1 && (c.ArrivedAt != "" || c.SkippedAt != "") {
				return fmt.Errorf("stop %s was settled by a pin (arrived %q, skipped %q)", c.Name, c.ArrivedAt, c.SkippedAt)
			}
		}
		return nil
	})
	sc.Step(`^the pin made no post$`, func() error {
		if n := w.postCount(); n != w.before {
			return fmt.Errorf("a pin made a post: %d post(s) before, %d after", w.before, n)
		}
		return nil
	})
	sc.Step(`^the Ride map carries one pin$`, func() error { return w.mapPins(1) })
	sc.Step(`^the Ride map carries no pin$`, func() error { return w.mapPins(0) })
}

func (w *dropPinWorld) open(c *http.Cookie, path string) error {
	rec := doAs(w.p.t, w.p.sim.app, c, "GET", path, nil)
	if rec.Code >= 400 {
		return fmt.Errorf("GET %s answered %d", path, rec.Code)
	}
	w.page = rec.Body.String()
	return nil
}

// header is the page's masthead, where the button belongs.
func (w *dropPinWorld) header() string {
	i, j := strings.Index(w.page, `<header class="masthead">`), strings.Index(w.page, `</header>`)
	if i < 0 || j < i {
		return ""
	}
	return w.page[i:j]
}

// postCount is the census of every post, as the trip-keeper reads it.
func (w *dropPinWorld) postCount() int {
	var doc struct {
		Count int `json:"count"`
	}
	rec := doAs(w.p.t, w.p.sim.app, w.p.sim.cookies["kris"], "GET", "/driver/posts.json", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	return doc.Count
}

func (w *dropPinWorld) drop(c *http.Cookie) error {
	w.before = w.postCount()
	form := url.Values{"lat": {strconv.FormatFloat(stopLat(3), 'f', -1, 64)},
		"lng": {strconv.FormatFloat(stopsLng, 'f', -1, 64)}, "accuracy": {"5"}}
	w.last = doAs(w.p.t, w.p.sim.app, c, "POST", "/driver/pin", form)
	return nil
}

var rideMapRe = regexp.MustCompile(`window\.RIDE = (\{.*?\});</script>`)

func (w *dropPinWorld) mapPins(want int) error {
	page := doAs(w.p.t, w.p.sim.app, nil, "GET", "/", nil).Body.String()
	m := rideMapRe.FindStringSubmatch(page)
	if m == nil {
		return fmt.Errorf("the Ride page carries no map data")
	}
	var doc struct {
		Pins []struct {
			Lat, Lng float64
		} `json:"pins"`
	}
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
		return err
	}
	if len(doc.Pins) != want {
		return fmt.Errorf("the Ride map carries %d pins, want %d", len(doc.Pins), want)
	}
	return nil
}
