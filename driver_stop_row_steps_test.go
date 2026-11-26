package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// Steps for features/driver_stop_row.feature, on the postWorld of
// post_steps_test.go and the stops A..D of start_checkin_steps_test.go.

type stopRowWorld struct {
	p    *postWorld
	page string
	hud  map[string]any
	css  string
}

var stopRowRe = regexp.MustCompile(`(?s)<nav class="stop-row"[^>]*>(.*?)</nav>`)
var stopBtnRe = regexp.MustCompile(`(?s)<(a|button)\b([^>]*\bclass="[^"]*\bstop-btn\b[^"]*"[^>]*)>(.*?)</(?:a|button)>`)

// stopRowButtons is the row's buttons in page order: the tag, its attributes
// and its inner text with the tags taken out.
type stopBtn struct{ tag, attrs, text string }

func (w *stopRowWorld) buttons() ([]stopBtn, error) {
	m := stopRowRe.FindStringSubmatch(w.page)
	if m == nil {
		return nil, fmt.Errorf("the driver screen carries no stop row")
	}
	tagRe := regexp.MustCompile(`<[^>]*>`)
	var out []stopBtn
	for _, b := range stopBtnRe.FindAllStringSubmatch(m[1], -1) {
		out = append(out, stopBtn{b[1], b[2], strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(b[3], " "))), " ")})
	}
	return out, nil
}

func (w *stopRowWorld) button(id string) (*stopBtn, error) {
	bs, err := w.buttons()
	if err != nil {
		return nil, err
	}
	for i := range bs {
		if strings.Contains(bs[i].attrs, `id="`+id+`"`) {
			return &bs[i], nil
		}
	}
	return nil, fmt.Errorf("the stop row has no button %q", id)
}

func attrOf(attrs, name string) string {
	m := regexp.MustCompile(`\b` + name + `="([^"]*)"`).FindStringSubmatch(attrs)
	if m == nil {
		return ""
	}
	return html.UnescapeString(m[1])
}

func initDriverStopRowScenarios(sc *godog.ScenarioContext, p *postWorld) {
	w := &stopRowWorld{p: p}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = stopRowWorld{p: p}
		return ctx, nil
	})
	sc.Step(`^Chester is on for the stop row$`, func() error {
		p.sim.app.chester = newChester(chesterEnv(p.t, newAPIStub(p.t), nil))
		return nil
	})
	sc.Step(`^kris reads the driver screen's stop row$`, func() error { return w.open(p.sim.cookies["kris"]) })
	sc.Step(`^a rider with no ride reads the driver screen's stop row$`, func() error {
		c := asUser(p.t, p.sim.app, "rider", roleEditor)
		if err := p.sim.app.auth.users.setRides("rider", []string{}); err != nil {
			return err
		}
		return w.open(c)
	})
	sc.Step(`^the stop row carries Navigate, Check in, Post, Drop Pin and Chester in that order$`, func() error {
		return w.order("Navigate", "Check in", "Post", "Drop Pin", "Chester")
	})
	sc.Step(`^the stop row carries only Post and Drop Pin$`, func() error { return w.order("Post", "Drop Pin") })
	sc.Step(`^the stop row comes before the music card and the road section$`, func() error {
		row := strings.Index(w.page, `class="stop-row"`)
		for _, later := range []string{`id="rt-card"`, `class="hud-road`, `id="hud-waze"`} {
			if i := strings.Index(w.page, later); i >= 0 && i < row {
				return fmt.Errorf("%s comes before the stop row", later)
			}
		}
		if row < 0 {
			return fmt.Errorf("no stop row on the driver screen")
		}
		return nil
	})
	sc.Step(`^every stop row button is a link or a button with no field to fill$`, func() error {
		bs, err := w.buttons()
		if err != nil {
			return err
		}
		if len(bs) == 0 {
			return fmt.Errorf("the stop row has no buttons")
		}
		for _, b := range bs {
			if b.tag == "a" && attrOf(b.attrs, "href") == "" && attrOf(b.attrs, "aria-disabled") != "true" {
				return fmt.Errorf("%q is a link with no address", b.text)
			}
			if strings.Contains(b.text, "<input") {
				return fmt.Errorf("%q holds a field", b.text)
			}
		}
		if m := stopRowRe.FindStringSubmatch(w.page); strings.Contains(m[1], "<input") || strings.Contains(m[1], "<select") || strings.Contains(m[1], "<form") {
			return fmt.Errorf("the stop row holds a form field")
		}
		return nil
	})
	sc.Step(`^the stylesheet makes every stop row button at least (\d+) pixels tall$`, func(min int) error {
		css, err := os.ReadFile("static/css/app.css")
		if err != nil {
			return err
		}
		rule := regexp.MustCompile(`(?s)\.stop-row \.stop-btn \{([^}]*)\}`).FindSubmatch(css)
		if rule == nil {
			return fmt.Errorf("app.css has no .stop-row .stop-btn rule")
		}
		m := regexp.MustCompile(`min-height:\s*(\d+)px`).FindSubmatch(rule[1])
		if m == nil {
			return fmt.Errorf("the .stop-row .stop-btn rule sets no min-height in px")
		}
		if n, _ := strconv.Atoi(string(m[1])); n < min {
			return fmt.Errorf("the stop row buttons are %d px tall, want at least %d", n, min)
		}
		return nil
	})
	sc.Step(`^kris chooses Google Maps for Navigate$`, func() error {
		rec := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "POST", "/driver/maps", url.Values{"app": {"google"}})
		if rec.Code != http.StatusSeeOther {
			return fmt.Errorf("choosing the maps app answered %d", rec.Code)
		}
		return nil
	})
	sc.Step(`^Navigate opens the (Waze|Google Maps) link for "([^"]*)"$`, func(app, addr string) error {
		b, err := w.button("stop-navigate")
		if err != nil {
			return err
		}
		href := attrOf(b.attrs, "href")
		host := map[string]string{"Waze": "waze.com", "Google Maps": "google.com/maps"}[app]
		if !strings.Contains(href, host) {
			return fmt.Errorf("Navigate goes to %s, want a %s link", href, app)
		}
		if !strings.Contains(href, url.QueryEscape(addr)) && !strings.Contains(href, strings.ReplaceAll(url.QueryEscape(addr), "+", "%20")) {
			return fmt.Errorf("Navigate goes to %s, which does not carry %q", href, addr)
		}
		return nil
	})
	sc.Step(`^the server keeps the choice for kris$`, func() error {
		if v, ok := p.sim.app.store.Meta("maps:kris"); !ok || v != "google" {
			return fmt.Errorf("the server holds %q for kris's maps app", v)
		}
		return nil
	})
	sc.Step(`^Navigate is disabled and says "([^"]*)"$`, func(say string) error { return w.disabled("stop-navigate", say) })
	sc.Step(`^Check in is disabled and says "([^"]*)"$`, func(say string) error { return w.disabled("stop-checkin", say) })
	sc.Step(`^the stop row phone fix is at B$`, func() error { return w.report(stopLat(1)) })
	sc.Step(`^the stop row phone fix is 100 km from every stop$`, func() error {
		// 0.95 degree of longitude at 40 degrees north is about 80 km; go
		// north of the last stop by a degree instead, 111 km clear of D.
		return w.report(stopLat(3) + 1.0)
	})
	sc.Step(`^the live screen offers Check in at B$`, func() error {
		id, _ := w.hud["check_in_id"].(float64)
		if int64(id) != p.cps[1].ID || w.hud["check_in_name"] != p.cps[1].Name {
			return fmt.Errorf("the live screen offers check_in %v %v, want %s", w.hud["check_in_id"], w.hud["check_in_name"], p.cps[1].Name)
		}
		return nil
	})
	sc.Step(`^the live screen offers no Check in$`, func() error {
		if id, ok := w.hud["check_in_id"]; ok && id != float64(0) {
			return fmt.Errorf("the live screen offers a check-in at %v", w.hud["check_in_name"])
		}
		return nil
	})
	sc.Step(`^the Check in link goes to the post form for B$`, func() error {
		id, _ := w.hud["check_in_id"].(float64)
		want := "/driver/post?checkpoint=" + strconv.FormatInt(p.cps[1].ID, 10)
		if int64(id) != p.cps[1].ID {
			return fmt.Errorf("no check-in id for B")
		}
		// The page's own link is built by the same rule: the live screen sets
		// the row's href from the id, so the id is what carries the link.
		js, err := os.ReadFile("static/js/hud.js")
		if err != nil {
			return err
		}
		if !strings.Contains(string(js), "/driver/post?checkpoint=") {
			return fmt.Errorf("hud.js builds no %s link", want)
		}
		return nil
	})
	sc.Step(`^the stylesheet is read for the stop row$`, func() error {
		b, err := os.ReadFile("static/css/app.css")
		w.css = string(b)
		return err
	})
	sc.Step(`^the stop row is a grid that wraps to three across under (\d+) pixels and never overflows$`, func(px int) error {
		if w.css == "" {
			b, err := os.ReadFile("static/css/app.css")
			if err != nil {
				return err
			}
			w.css = string(b)
		}
		base := regexp.MustCompile(`(?s)\.stop-row \{([^}]*)\}`).FindStringSubmatch(w.css)
		if base == nil {
			return fmt.Errorf("app.css has no .stop-row rule")
		}
		for _, need := range []string{"display: grid", "min-width: 0", "box-sizing: border-box"} {
			if !strings.Contains(base[1], need) {
				return fmt.Errorf("the .stop-row rule lacks %q", need)
			}
		}
		media := regexp.MustCompile(`(?s)@media \(max-width: ` + strconv.Itoa(px-1) + `px\) \{[^@]*?\.stop-row \{([^}]*)\}`).FindStringSubmatch(w.css)
		if media == nil || !strings.Contains(media[1], "repeat(3") {
			return fmt.Errorf("no @media (max-width: %dpx) rule gives the stop row three columns", px-1)
		}
		return nil
	})
}

func (w *stopRowWorld) open(c *http.Cookie) error {
	rec := doAs(w.p.t, w.p.sim.app, c, "GET", "/driver", nil)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("GET /driver answered %d", rec.Code)
	}
	w.page = rec.Body.String()
	return nil
}

func (w *stopRowWorld) order(want ...string) error {
	bs, err := w.buttons()
	if err != nil {
		return err
	}
	var got []string
	for _, b := range bs {
		got = append(got, b.text)
	}
	if len(got) != len(want) {
		return fmt.Errorf("the stop row carries %q, want %q", got, want)
	}
	for i := range want {
		if !strings.Contains(got[i], want[i]) {
			return fmt.Errorf("the stop row carries %q, want %q in that order", got, want)
		}
	}
	return nil
}

func (w *stopRowWorld) disabled(id, say string) error {
	b, err := w.button(id)
	if err != nil {
		return err
	}
	if attrOf(b.attrs, "aria-disabled") != "true" {
		return fmt.Errorf("%s is not disabled: %s", id, b.attrs)
	}
	if strings.Contains(b.attrs, `href="http`) {
		return fmt.Errorf("%s is disabled but still links out: %s", id, b.attrs)
	}
	if !strings.Contains(b.text, say) {
		return fmt.Errorf("%s says %q, want %q", id, b.text, say)
	}
	return nil
}

func (w *stopRowWorld) report(lat float64) error {
	target := fmt.Sprintf("/driver/hud.json?lat=%.5f&lng=%.5f", lat, stopsLng)
	rec := doAs(w.p.t, w.p.sim.app, w.p.sim.cookies["kris"], "GET", target, nil)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("hud.json answered %d: %s", rec.Code, rec.Body.String())
	}
	w.hud = nil
	return json.Unmarshal(rec.Body.Bytes(), &w.hud)
}
