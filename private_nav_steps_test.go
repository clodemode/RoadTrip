package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// Steps for features/private_stop_navigation.feature. They ride the
// start-and-check-in world (stops A to D, one leg). The private address is
// written to the test app's private.json overlay, as the lodging page does.
// Every place and address is invented.
type privNavWorld struct {
	n   *navWorld
	hud map[string]any
	// visitor holds every body a visitor read.
	visitor map[string]string
	addr    string
	admin   map[string]string
}

func initPrivateNavScenarios(sc *godog.ScenarioContext, p *postWorld) {
	w := &privNavWorld{n: &navWorld{p: p}}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = privNavWorld{n: &navWorld{p: p}}
		return ctx, nil
	})
	sc.Step(`^([ABCD]) has the private address "([^"]*)"$`, w.setAddress)
	sc.Step(`^([ABCD]) has the private address "([^"]*)" filed for its own name$`, func(s, addr string) error {
		return w.setAddressFor(s, addr, w.name(s))
	})
	sc.Step(`^([ABCD]) has the private address "([^"]*)" filed for the stop "([^"]*)"$`, w.setAddressFor)
	sc.Step(`^kris opens the post form and the Lodging screen$`, w.openAdmin)
	sc.Step(`^neither shows the address$`, func() error {
		for path, body := range w.admin {
			if carries(body, w.addr) {
				return fmt.Errorf("%s shows the address", path)
			}
		}
		return nil
	})
	sc.Step(`^kris saves the Lodging screen as it stands$`, w.saveLodging)
	sc.Step(`^the overlay still holds the address filed for "([^"]*)"$`, w.overlayHolds)
	sc.Step(`^kris reads the driver screen's navigation$`, w.readHUD)
	sc.Step(`^the driver links for Waze, Google Maps and Apple Maps carry the address of ([ABCD])$`, func(s string) error {
		return w.hudLinksCarry(true, "")
	})
	sc.Step(`^the driver links go to ([ABCD]) by its name and carry no coordinates$`, func(s string) error {
		return w.hudLinksCarry(false, w.name(s))
	})
	sc.Step(`^the driver links go to ([ABCD]) by its coordinates$`, func(s string) error {
		c := p.cps[strings.Index("ABCD", s)]
		pt := strconv.FormatFloat(c.Lat, 'f', -1, 64) + "," + strconv.FormatFloat(c.Lng, 'f', -1, 64)
		for _, k := range []string{"waze", "google", "apple"} {
			l, _ := w.hud[k].(string)
			if !strings.Contains(l, pt) && !strings.Contains(l, url.QueryEscape(pt)) {
				return fmt.Errorf("the %s link does not carry %s: %s", k, pt, l)
			}
		}
		return nil
	})
	sc.Step(`^the Ride row for ([ABCD]) has Waze, Google Maps and Apple Maps links that carry the address of ([ABCD])$`, func(s, _ string) error {
		r, err := w.n.row(s)
		if err != nil {
			return err
		}
		return w.allThreeCarry(hrefRe.FindAllStringSubmatch(r, -1))
	})
	sc.Step(`^the day's end links carry the address of ([ABCD])$`, func(string) error {
		b := dayEndRe.FindString(w.n.page)
		if b == "" {
			return fmt.Errorf("the page has no day's end block")
		}
		return w.allThreeCarry(hrefRe.FindAllStringSubmatch(b, -1))
	})
	sc.Step(`^a visitor reads every page and JSON the site offers$`, w.visitorReads)
	sc.Step(`^none of them carries the address of ([ABCD])$`, func(s string) error {
		// Positive control: the visitor's Ride page is a real page that names the stop.
		if !strings.Contains(w.visitor["/"], w.name(s)) {
			return fmt.Errorf("the visitor's Ride page does not name %s, so the check below could not fire", w.name(s))
		}
		for path, body := range w.visitor {
			if carries(body, w.addr) {
				return fmt.Errorf("%s carries the address", path)
			}
		}
		return nil
	})
	sc.Step(`^kris reads the Ride page's navigation$`, func() error { return w.n.get("/", true) })
}

func (w *privNavWorld) name(s string) string { return w.n.p.cps[strings.Index("ABCD", s)].Name }

func (w *privNavWorld) setAddress(s, addr string) error { return w.setAddressFor(s, addr, "") }

func (w *privNavWorld) setAddressFor(s, addr, forName string) error {
	app := w.n.p.sim.app
	rv, err := app.loadRide()
	if err != nil {
		return err
	}
	pd, err := loadPrivate(app.dataDir)
	if err != nil {
		return err
	}
	cp := w.n.p.cps[strings.Index("ABCD", s)]
	cps := map[string]privateCheckpoint{}
	for k, v := range pd.forRide(rv.Ride.Slug, app.rideSlug).Checkpoints {
		cps[k] = v
	}
	cps[strconv.Itoa(cp.Ord)] = privateCheckpoint{Address: addr, For: forName}
	pd.setRide(rv.Ride.Slug, app.rideSlug, cps)
	w.addr = addr
	return savePrivate(app.dataDir, pd)
}

func (w *privNavWorld) readHUD() error {
	rec := doAs(w.n.p.t, w.n.p.sim.app, w.n.p.sim.cookies["kris"], "GET", "/driver/hud.json", nil)
	if rec.Code != 200 {
		return fmt.Errorf("hud.json answered %d", rec.Code)
	}
	w.hud = nil
	return json.Unmarshal(rec.Body.Bytes(), &w.hud)
}

// carries says whether body holds addr plain, HTML-escaped or URL-escaped.
// The body is read with its HTML entities undone first, as a browser would
// (a + in a link is written &#43; on the page).
func carries(body, addr string) bool {
	body = html.UnescapeString(body)
	for _, f := range []string{addr, html.EscapeString(addr), url.QueryEscape(addr), url.PathEscape(addr), strings.ReplaceAll(url.QueryEscape(addr), "+", "%20")} {
		if strings.Contains(body, f) {
			return true
		}
	}
	return false
}

func (w *privNavWorld) hudLinksCarry(addr bool, name string) error {
	for _, k := range []string{"waze", "google", "apple"} {
		l, _ := w.hud[k].(string)
		if addr {
			if !carries(l, w.addr) {
				return fmt.Errorf("the %s link does not carry the address: %s", k, l)
			}
			continue
		}
		if !carries(l, name) || coordRe.MatchString(l) {
			return fmt.Errorf("the %s link is not the bare name %q: %s", k, name, l)
		}
	}
	return nil
}

var navHosts = []string{"https://waze.com/ul", "https://www.google.com/maps/dir", "https://maps.apple.com/"}

func (w *privNavWorld) allThreeCarry(ms [][]string) error {
	seen := map[string]bool{}
	for _, m := range ms {
		l := html.UnescapeString(m[1])
		for _, h := range navHosts {
			if strings.HasPrefix(l, h) {
				if !carries(l, w.addr) {
					return fmt.Errorf("a %s link does not carry the address: %s", h, l)
				}
				seen[h] = true
			}
		}
	}
	for _, h := range navHosts {
		if !seen[h] {
			return fmt.Errorf("no %s link found", h)
		}
	}
	return nil
}

func (w *privNavWorld) visitorReads() error {
	app := w.n.p.sim.app
	rv, err := app.loadRide()
	if err != nil {
		return err
	}
	paths := []string{"/", "/plan.json", "/tracks.json", "/route.geojson", "/driver/hud.json", "/driver/lodging", "/legs", "/photos", "/chester"}
	for _, l := range rv.Legs {
		paths = append(paths, "/legs/"+l.Slug)
	}
	w.visitor = map[string]string{}
	for _, p := range paths {
		rec := doAs(w.n.p.t, app, nil, "GET", p, nil)
		w.visitor[p] = rec.Body.String()
	}
	return nil
}

func (w *privNavWorld) openAdmin() error {
	w.admin = map[string]string{}
	for _, p := range []string{"/driver/post", "/driver/lodging"} {
		rec := doAs(w.n.p.t, w.n.p.sim.app, w.n.p.sim.cookies["kris"], "GET", p, nil)
		if rec.Code != 200 {
			return fmt.Errorf("%s answered %d", p, rec.Code)
		}
		w.admin[p] = rec.Body.String()
	}
	return nil
}

// saveLodging posts the Lodging form the way the page would: every stop's
// address and notes as the screen shows them.
func (w *privNavWorld) saveLodging() error {
	app := w.n.p.sim.app
	rv, err := app.loadRide()
	if err != nil {
		return err
	}
	pd, err := loadPrivate(app.dataDir)
	if err != nil {
		return err
	}
	shown := pd.forRide(rv.Ride.Slug, app.rideSlug).onPlan(rv.Legs).byOrd()
	form := url.Values{}
	for _, l := range rv.Legs {
		for _, c := range l.Checkpoints {
			form.Set("address-"+strconv.Itoa(c.Ord), shown[c.Ord].Address)
			form.Set("notes-"+strconv.Itoa(c.Ord), shown[c.Ord].Notes)
		}
	}
	rec := doAs(w.n.p.t, app, w.n.p.sim.cookies["kris"], "POST", "/driver/lodging", form)
	if rec.Code != 303 {
		return fmt.Errorf("the Lodging save answered %d", rec.Code)
	}
	return nil
}

func (w *privNavWorld) overlayHolds(forName string) error {
	app := w.n.p.sim.app
	rv, err := app.loadRide()
	if err != nil {
		return err
	}
	pd, err := loadPrivate(app.dataDir)
	if err != nil {
		return err
	}
	for _, e := range pd.forRide(rv.Ride.Slug, app.rideSlug).Checkpoints {
		if e.For == forName && e.Address == w.addr {
			return nil
		}
	}
	return fmt.Errorf("the overlay no longer holds the address filed for %q", forName)
}
