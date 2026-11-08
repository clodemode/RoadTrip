package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/start_and_done_stops.feature. They ride the start-and-
// check-in world (stops A to D on a line) and read what a signed-in driver
// is shown: a stop row on the trip page or a leg page, the driver screen and
// its figures, what Chester is told and the open route data.
type doneWorld struct {
	p    *postWorld
	page string
	hud  map[string]any
	cx   *chesterContext
	geo  []map[string]any
}

var (
	rowRe    = regexp.MustCompile(`(?s)<li class="cp[^"]*"[^>]*>.*?</li>`)
	rowCls   = regexp.MustCompile(`<li class="([^"]*)"`)
	statusRe = regexp.MustCompile(`<span class="(?:arrived-at|skipped-at)[^"]*">([^<]*)</span>`)
	navRe    = regexp.MustCompile(`href="https://(?:waze\.com/ul|www\.google\.com/maps/dir|maps\.apple\.com)`)
	arriveBt = regexp.MustCompile(`>\s*Arrived\s*<`)
)

func initDoneStopsScenarios(sc *godog.ScenarioContext, p *postWorld) {
	d := &doneWorld{p: p}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*d = doneWorld{p: p}
		return ctx, nil
	})
	sc.Step(`^kris opens the trip page$`, func() error { return d.get("/") })
	sc.Step(`^kris opens the first leg's page$`, d.legPage)
	sc.Step(`^the row for ([ABCD]) says it is where the drive leaves from$`, func(w string) error { return d.rowHas(w, "Leaving from here") })
	sc.Step(`^the row for ([ABCD]) offers "Start the drive"$`, d.offersStart)
	sc.Step(`^the row for ([ABCD]) offers "Ask Chester about here"$`, func(w string) error { return d.rowHas(w, "Ask Chester about here") })
	sc.Step(`^the row for ([ABCD]) offers no navigation$`, d.noNav)
	sc.Step(`^no row offers "Arrived"$`, d.noArrivedButton)
	sc.Step(`^the row for ([ABCD]) says "(Left|Arrived|Missed)"$`, d.says)
	sc.Step(`^the row for ([ABCD]) says "(Left|Arrived|Missed)" and offers no navigation$`, func(w, s string) error {
		if err := d.says(w, s); err != nil {
			return err
		}
		return d.noNav(w)
	})
	sc.Step(`^the row for ([ABCD]) is marked as next and offers navigation$`, func(w string) error {
		if err := d.isNext(w); err != nil {
			return err
		}
		return d.hasNav(w)
	})
	sc.Step(`^the row for ([ABCD]) is marked as next$`, d.isNext)
	sc.Step(`^the active trip has two days and the first stop of day two is arrived at$`, d.twoDays)
	sc.Step(`^the first stop of day two says "Arrived", not "Left"$`, d.dayTwo)
	sc.Step(`^kris undoes ([ABCD])$`, d.undo)
	sc.Step(`^([ABCD]) is not skipped$`, func(w string) error {
		if c := p.cp(strings.Index("ABCD", w)); c.SkippedAt != "" || c.SkipReason != "" {
			return fmt.Errorf("%s is still skipped: %q %q", c.Name, c.SkippedAt, c.SkipReason)
		}
		return nil
	})
	sc.Step(`^kris opens the driver screen$`, func() error { return d.get("/driver") })
	sc.Step(`^the screen says the drive starts at A$`, func() error {
		if !strings.Contains(html.UnescapeString(d.page), "Start · "+p.cps[0].Name) {
			return fmt.Errorf("the driver screen does not say Start · %s", p.cps[0].Name)
		}
		return nil
	})
	sc.Step(`^the screen does not call A the next stop$`, func() error {
		if strings.Contains(d.page, `class="hud-k">Next<`) {
			return fmt.Errorf("the driver screen labels something Next before the drive has started")
		}
		return nil
	})
	sc.Step(`^the screen offers no navigation to A$`, func() error {
		if strings.Contains(d.page, `id="hud-next">`+p.cps[0].Name) && !strings.Contains(d.page, `class="hud-k">Start<`) {
			return fmt.Errorf("A is the headline without the Start label")
		}
		for _, m := range regexp.MustCompile(`href="(https://(?:waze\.com/ul|www\.google\.com/maps/dir|maps\.apple\.com)[^"]*)"`).FindAllStringSubmatch(d.page, -1) {
			if strings.Contains(m[1], url.QueryEscape(p.cps[0].Name)) || strings.Contains(m[1], p.cps[0].Name) {
				return fmt.Errorf("the screen links to navigation to A: %s", m[1])
			}
		}
		// Nor does the page hold live navigation links to anywhere before the
		// drive has started: the nav block is hidden and carries no target.
		if navRe.MatchString(d.page) {
			return fmt.Errorf("the screen carries a navigation link before the drive has started")
		}
		return nil
	})
	sc.Step(`^kris asks for the driver screen's figures$`, d.figures)
	sc.Step(`^the next stop is ([ABCD])$`, func(w string) error {
		if got := d.hud["next_name"]; got != p.cps[strings.Index("ABCD", w)].Name {
			return fmt.Errorf("next_name is %v", got)
		}
		return nil
	})
	sc.Step(`^the drive is reported as started$`, func() error {
		if d.hud["drive_started"] != true {
			return fmt.Errorf("drive_started is %v", d.hud["drive_started"])
		}
		return nil
	})
	sc.Step(`^Chester's context is built$`, func() error {
		cx, err := p.sim.app.chesterContext(p.sim.now, nil, nil)
		d.cx = cx
		return err
	})
	sc.Step(`^it says A was left, not arrived at$`, func() error {
		l := d.stableLine(0)
		if !strings.Contains(l, ", left ") || strings.Contains(l, "arrived") {
			return fmt.Errorf("A's line: %q", l)
		}
		return nil
	})
	sc.Step(`^it says B was passed without stopping$`, func() error {
		l := d.stableLine(1)
		if !strings.Contains(l, "passed without stopping (missed)") || strings.Contains(l, "arrived") {
			return fmt.Errorf("B's line: %q", l)
		}
		return nil
	})
	sc.Step(`^it names C as the next stop$`, func() error {
		if want := "NEXT STOP: " + p.cps[2].Name; !strings.Contains(d.cx.Volatile, want) {
			return fmt.Errorf("the volatile block lacks %q:\n%s", want, d.cx.Volatile)
		}
		return nil
	})
	sc.Step(`^kris reads the route data$`, d.routeData)
	sc.Step(`^A is marked as the start$`, func() error { return d.prop(0, "is_start", true) })
	sc.Step(`^B carries its skip and its reason$`, func() error {
		f := d.feature(1)
		if f["skipped_at"] == nil || f["skipped_at"] == "" || f["skip_reason"] != "missed" {
			return fmt.Errorf("B's properties: %v", f)
		}
		return nil
	})
	sc.Step(`^C carries neither an arrival nor a skip$`, func() error {
		f := d.feature(2)
		for _, k := range []string{"arrived_at", "skipped_at"} {
			if v, ok := f[k]; ok && v != "" {
				return fmt.Errorf("C carries %s %v", k, v)
			}
		}
		return nil
	})
}

func (d *doneWorld) get(path string) error {
	w := doAs(d.p.t, d.p.sim.app, d.p.sim.cookies["kris"], "GET", path, nil)
	if w.Code != http.StatusOK {
		return fmt.Errorf("%s answered %d", path, w.Code)
	}
	d.page = w.Body.String()
	return nil
}

func (d *doneWorld) legPage() error {
	rv, err := d.p.sim.app.loadRide()
	if err != nil {
		return err
	}
	return d.get("/legs/" + rv.Legs[0].Slug)
}

// row is the <li> of the stop named for letter w, from the page last read.
func (d *doneWorld) row(w string) (string, error) {
	name := d.p.cps[strings.Index("ABCD", w)].Name
	for _, r := range rowRe.FindAllString(d.page, -1) {
		if strings.Contains(r, `<span class="cp-name">`+name+`</span>`) {
			return r, nil
		}
	}
	return "", fmt.Errorf("the page has no row for %s", name)
}

func (d *doneWorld) rowHas(w, want string) error {
	r, err := d.row(w)
	if err != nil {
		return err
	}
	if !strings.Contains(r, want) {
		return fmt.Errorf("the row for %s does not say %q: %s", w, want, r)
	}
	return nil
}

func (d *doneWorld) offersStart(w string) error {
	r, err := d.row(w)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`<a [^>]*href="/driver/post"[^>]*>\s*Start the drive\s*</a>`).MatchString(r) {
		return fmt.Errorf("the row for %s has no Start the drive link to /driver/post: %s", w, r)
	}
	return nil
}

func (d *doneWorld) noNav(w string) error {
	r, err := d.row(w)
	if err != nil {
		return err
	}
	if navRe.MatchString(r) || strings.Contains(r, "Navigate in Waze") {
		return fmt.Errorf("the row for %s offers navigation: %s", w, r)
	}
	return nil
}

func (d *doneWorld) hasNav(w string) error {
	r, err := d.row(w)
	if err != nil {
		return err
	}
	for _, h := range []string{"https://waze.com/ul", "https://www.google.com/maps/dir", "https://maps.apple.com/"} {
		if !strings.Contains(r, `href="`+h) {
			return fmt.Errorf("the row for %s lacks a %s link: %s", w, h, r)
		}
	}
	return nil
}

func (d *doneWorld) isNext(w string) error {
	r, err := d.row(w)
	if err != nil {
		return err
	}
	m := rowCls.FindStringSubmatch(r)
	if m == nil || !strings.Contains(" "+m[1]+" ", " next ") {
		return fmt.Errorf("the row for %s is not marked next: %s", w, r)
	}
	return nil
}

func (d *doneWorld) noArrivedButton() error {
	if arriveBt.MatchString(d.page) {
		return fmt.Errorf("the page has an Arrived button")
	}
	return nil
}

func (d *doneWorld) status(r string) string {
	if m := statusRe.FindStringSubmatch(r); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func (d *doneWorld) says(w, want string) error {
	r, err := d.row(w)
	if err != nil {
		return err
	}
	if s := d.status(r); !strings.HasPrefix(s, want) {
		return fmt.Errorf("the row for %s says %q, want %q", w, s, want)
	}
	return nil
}

// twoDays swaps the trip for the two-day one of the marks feature and stamps
// the first stop of day two.
func (d *doneWorld) twoDays() error {
	m := &marksWorld{t: d.p.t, sim: d.p.sim}
	if err := m.twoDays(); err != nil {
		return err
	}
	rv, err := d.p.sim.app.loadRide()
	if err != nil {
		return err
	}
	d.p.cps = rideCheckpoints(rv.Legs)
	if len(rv.Legs) != 2 || len(rv.Legs[1].Checkpoints) == 0 {
		return fmt.Errorf("want two days, have %d", len(rv.Legs))
	}
	return d.p.sim.app.store.SetArrived(rv.Legs[1].Checkpoints[0].ID, time.Date(2026, 11, 6, 14, 0, 0, 0, time.UTC), nil)
}

func (d *doneWorld) dayTwo() error {
	rv, err := d.p.sim.app.loadRide()
	if err != nil {
		return err
	}
	name := rv.Legs[1].Checkpoints[0].Name
	for _, r := range rowRe.FindAllString(d.page, -1) {
		if strings.Contains(r, `<span class="cp-name">`+name+`</span>`) {
			if s := d.status(r); !strings.HasPrefix(s, "Arrived") {
				return fmt.Errorf("%s says %q", name, s)
			}
			return nil
		}
	}
	return fmt.Errorf("no row for %s", name)
}

// undo reads the row's own Undo form and sends it as the button would: the
// clear flag and no position.
func (d *doneWorld) undo(w string) error {
	if err := d.get("/"); err != nil {
		return err
	}
	r, err := d.row(w)
	if err != nil {
		return err
	}
	id := strconv.FormatInt(d.p.cps[strings.Index("ABCD", w)].ID, 10)
	form := regexp.MustCompile(`(?s)<form[^>]*action="/driver/checkpoint/` + id + `/arrive"[^>]*>(.*?)</form>`).FindStringSubmatch(r)
	if form == nil || !strings.Contains(form[1], `name="clear" value="1"`) || !strings.Contains(form[1], "Undo") {
		return fmt.Errorf("the row for %s has no Undo form: %s", w, r)
	}
	if strings.Contains(form[1], `name="lat"`) || strings.Contains(form[1], `name="lng"`) {
		return fmt.Errorf("the Undo form for %s carries a position", w)
	}
	res := doAs(d.p.t, d.p.sim.app, d.p.sim.cookies["kris"], "POST", "/driver/checkpoint/"+id+"/arrive", url.Values{"clear": {"1"}})
	if res.Code != http.StatusSeeOther {
		return fmt.Errorf("undo answered %d: %s", res.Code, res.Body.String())
	}
	return d.get("/")
}

func (d *doneWorld) figures() error {
	w := doAs(d.p.t, d.p.sim.app, d.p.sim.cookies["kris"], "GET", "/driver/hud.json", nil)
	if w.Code != http.StatusOK {
		return fmt.Errorf("hud.json answered %d", w.Code)
	}
	d.hud = nil
	return json.Unmarshal(w.Body.Bytes(), &d.hud)
}

// stableLine is the line of Chester's plan block for stop i.
func (d *doneWorld) stableLine(i int) string {
	for _, l := range strings.Split(d.cx.Stable, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "- "+d.p.cps[i].Name) {
			return l
		}
	}
	return ""
}

func (d *doneWorld) routeData() error {
	w := doAs(d.p.t, d.p.sim.app, d.p.sim.cookies["kris"], "GET", "/route.geojson", nil)
	if w.Code != http.StatusOK {
		return fmt.Errorf("route.geojson answered %d", w.Code)
	}
	var fc struct {
		Features []struct {
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &fc); err != nil {
		return err
	}
	d.geo = nil
	for _, f := range fc.Features {
		if f.Properties["kind"] == "checkpoint" {
			d.geo = append(d.geo, f.Properties)
		}
	}
	return nil
}

func (d *doneWorld) feature(i int) map[string]any {
	for _, f := range d.geo {
		if f["id"] == float64(d.p.cps[i].ID) {
			return f
		}
	}
	return map[string]any{}
}

func (d *doneWorld) prop(i int, key string, want any) error {
	if got := d.feature(i)[key]; got != want {
		return fmt.Errorf("%s of %s is %v, want %v", key, d.p.cps[i].Name, got, want)
	}
	return nil
}
