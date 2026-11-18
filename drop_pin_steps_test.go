package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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
var dropPinTagRe = regexp.MustCompile(`<button[^>]*id="drop-pin"[^>]*>`)

// dropPinColours reads the background and ink of the masthead's Drop Pin rule
// in app.css, both as #rrggbb.
func dropPinColours() (bg, ink string, err error) {
	css, err := os.ReadFile("static/css/app.css")
	if err != nil {
		return "", "", err
	}
	rule := regexp.MustCompile(`(?s)\.mast-links \.drop-pin \{([^}]*)\}`).FindSubmatch(css)
	if rule == nil {
		return "", "", fmt.Errorf("app.css has no .mast-links .drop-pin rule")
	}
	b := regexp.MustCompile(`background:\s*(#[0-9a-fA-F]{6})`).FindSubmatch(rule[1])
	c := regexp.MustCompile(`(?:^|[;\s])color:\s*(#[0-9a-fA-F]{6})`).FindSubmatch(rule[1])
	if b == nil || c == nil {
		return "", "", fmt.Errorf("the .mast-links .drop-pin rule sets no #rrggbb background and color: %s", rule[1])
	}
	return string(b[1]), string(c[1]), nil
}

// markerOf reads one kind of map marker's size and stacking from map.js: each
// kind's icon is built from a divIcon whose className names it, with an
// iconSize and a zIndexOffset beside it.
func markerOf(js, kind string) (size, z int, err error) {
	class := map[string]string{"photo": "post-pin has-photo", "note": "post-pin note-pin", "music note": "np-pin", "dropped pin": "drop-pin-dot"}[kind]
	re := regexp.MustCompile(`(?s)className: '` + regexp.QuoteMeta(class) + `'.*?iconSize: \[(\d+), \d+\].*?zIndexOffset: (\d+)`)
	m := re.FindStringSubmatch(js)
	if m == nil {
		return 0, 0, fmt.Errorf("map.js builds no %s marker with className %q, an iconSize and a zIndexOffset", kind, class)
	}
	size, _ = strconv.Atoi(m[1])
	z, _ = strconv.Atoi(m[2])
	return size, z, nil
}

// relLuminance is the WCAG relative luminance of a #rrggbb colour.
func relLuminance(hex string) float64 {
	ch := func(i int) float64 {
		v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(1) + 0.7152*ch(3) + 0.0722*ch(5)
}

func contrastRatio(a, b string) float64 {
	la, lb := relLuminance(a), relLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

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
	sc.Step(`^the Drop Pin button carries no dark outline style$`, func() error {
		btn := dropPinTagRe.FindString(w.header())
		if btn == "" {
			return fmt.Errorf("the header carries no Drop Pin button")
		}
		if strings.Contains(btn, "btn-outline-dark") {
			return fmt.Errorf("the Drop Pin button is still a dark outline: %s", btn)
		}
		return nil
	})
	sc.Step(`^the Drop Pin button's words contrast with its own background at least 7 to 1$`, func() error {
		bg, ink, err := dropPinColours()
		if err != nil {
			return err
		}
		if r := contrastRatio(bg, ink); r < 7 {
			return fmt.Errorf("the Drop Pin button's words are %.1f to 1 against its background, want at least 7", r)
		}
		return nil
	})
	sc.Step(`^the Drop Pin button's background is light against the dark masthead$`, func() error {
		bg, _, err := dropPinColours()
		if err != nil {
			return err
		}
		if l := relLuminance(bg); l < 0.7 {
			return fmt.Errorf("the Drop Pin button's background %s is not light (luminance %.2f)", bg, l)
		}
		return nil
	})
	var mapJS string
	sc.Step(`^the Ride map's script is read$`, func() error {
		b, err := os.ReadFile("static/js/map.js")
		mapJS = string(b)
		return err
	})
	sc.Step(`^the (photo|note|music note|dropped pin) marker is at least (\d+) px across$`, func(kind string, min int) error {
		size, _, err := markerOf(mapJS, kind)
		if err != nil {
			return err
		}
		if size < min {
			return fmt.Errorf("the %s marker is %d px across, want at least %d", kind, size, min)
		}
		return nil
	})
	sc.Step(`^the (photo|note|music note|dropped pin) marker is at most (\d+) px across$`, func(kind string, max int) error {
		size, _, err := markerOf(mapJS, kind)
		if err != nil {
			return err
		}
		if size > max {
			return fmt.Errorf("the %s marker is %d px across, want at most %d", kind, size, max)
		}
		return nil
	})
	sc.Step(`^the (photo|note|music note|dropped pin) marker is drawn above the (photo|note|music note|dropped pin) marker$`, func(top, under string) error {
		_, zt, err := markerOf(mapJS, top)
		if err != nil {
			return err
		}
		_, zu, err := markerOf(mapJS, under)
		if err != nil {
			return err
		}
		if zt <= zu {
			return fmt.Errorf("the %s marker (zIndexOffset %d) is not above the %s marker (%d)", top, zt, under, zu)
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
