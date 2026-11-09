package main

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// Steps for features/ride_page_checkin_and_navigate.feature. They ride the
// start-and-check-in world (stops A to D on one meridian, one leg) and read
// the Ride page, a leg page and the check-in post form as the signed-in
// trip-keeper, and the Ride page as a visitor. Every place is invented.
type navWorld struct {
	p    *postWorld
	page string
	tt   *ttStub
	add  url.Values // the hidden Add form the page offered for a found place
}

var (
	dayEndRe   = regexp.MustCompile(`(?s)<section class="dayend[^"]*"[^>]*>.*?</section>`)
	hrefRe     = regexp.MustCompile(`href="(https://(?:waze\.com/ul|www\.google\.com/maps/dir|maps\.apple\.com)[^"]*)"`)
	coordRe    = regexp.MustCompile(`-?\d+(?:\.\d+)?(?:,|%2C)-?\d+`)
	addFormRe  = regexp.MustCompile(`(?s)<form[^>]*action="/driver/routing"[^>]*>(.*?)</form>`)
	hiddenRe   = regexp.MustCompile(`<input type="hidden" name="([a-z]+)" value="([^"]*)">`)
	selectedRe = `<option value="%d"[^>]*selected`
)

func initRideNavScenarios(sc *godog.ScenarioContext, p *postWorld) {
	n := &navWorld{p: p}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*n = navWorld{p: p}
		return ctx, nil
	})
	sc.Step(`^kris reads the Ride page$`, func() error { return n.get("/", true) })
	sc.Step(`^kris reads the first leg's page$`, func() error {
		rv, err := p.sim.app.loadRide()
		if err != nil {
			return err
		}
		return n.get("/legs/"+rv.Legs[0].Slug, true)
	})
	sc.Step(`^a visitor reads the Ride page$`, func() error { return n.get("/", false) })
	sc.Step(`^no Ride row offers Check in$`, func() error {
		for _, r := range rowRe.FindAllString(n.page, -1) {
			if strings.Contains(r, "Check in") || strings.Contains(r, "/driver/post?checkpoint=") {
				return fmt.Errorf("a row offers Check in: %s", r)
			}
		}
		return nil
	})
	sc.Step(`^the Ride row for ([ABCD]) offers Start the drive$`, func(w string) error { return n.rowHas(w, "Start the drive") })
	sc.Step(`^the Ride row for ([ABCD]) offers Check in to its own post form$`, n.offersCheckIn)
	sc.Step(`^the Ride row for ([ABCD]) offers no Check in$`, func(w string) error {
		r, err := n.row(w)
		if err != nil {
			return err
		}
		if strings.Contains(r, "Check in") || strings.Contains(r, "/driver/post?checkpoint=") {
			return fmt.Errorf("the row for %s offers Check in: %s", w, r)
		}
		return nil
	})
	sc.Step(`^the Ride row for ([ABCD]) offers navigation$`, func(w string) error {
		r, err := n.row(w)
		if err != nil {
			return err
		}
		for _, h := range []string{"https://waze.com/ul", "https://www.google.com/maps/dir", "https://maps.apple.com/"} {
			if !strings.Contains(r, `href="`+h) {
				return fmt.Errorf("the row for %s lacks a %s link: %s", w, h, r)
			}
		}
		return nil
	})
	sc.Step(`^the Ride row for ([ABCD]) offers no navigation$`, func(w string) error {
		r, err := n.row(w)
		if err != nil {
			return err
		}
		if navRe.MatchString(r) {
			return fmt.Errorf("the row for %s offers navigation: %s", w, r)
		}
		return nil
	})
	sc.Step(`^kris opens ([ABCD])'s check-in post$`, func(w string) error {
		return n.get("/driver/post?checkpoint="+strconv.FormatInt(n.id(w), 10), true)
	})
	sc.Step(`^the form says it is checking in at ([ABCD])$`, func(w string) error {
		want := "Checking in at"
		if !strings.Contains(n.page, want) || !strings.Contains(n.page, p.cps[strings.Index("ABCD", w)].Name) {
			return fmt.Errorf("the form does not say %q %s", want, p.cps[strings.Index("ABCD", w)].Name)
		}
		return nil
	})
	sc.Step(`^the form pre-selects ([ABCD])$`, func(w string) error {
		if !regexp.MustCompile(fmt.Sprintf(selectedRe, n.id(w))).MatchString(n.page) {
			return fmt.Errorf("the form does not pre-select %s", w)
		}
		return nil
	})
	sc.Step(`^kris posts that photo through ([ABCD])'s check-in post$`, n.postThrough)
	sc.Step(`^the page offers Navigate to day's end at ([ABCD])$`, func(w string) error {
		b := dayEndRe.FindString(n.page)
		name := p.cps[strings.Index("ABCD", w)].Name
		if b == "" || !strings.Contains(b, "Day's end") && !strings.Contains(b, "Day&#39;s end") || !strings.Contains(b, name) {
			return fmt.Errorf("the page has no day's end block naming %s: %q", name, b)
		}
		return nil
	})
	sc.Step(`^the page offers no day's end$`, func() error {
		if dayEndRe.MatchString(n.page) {
			return fmt.Errorf("the page offers a day's end")
		}
		return nil
	})
	sc.Step(`^the day's end links go to ([ABCD]) in Waze, Google Maps and Apple Maps$`, func(w string) error {
		c := p.cps[strings.Index("ABCD", w)]
		pt := strconv.FormatFloat(c.Lat, 'f', -1, 64) + "," + strconv.FormatFloat(c.Lng, 'f', -1, 64)
		links, err := n.dayEndLinks()
		if err != nil {
			return err
		}
		for _, h := range []string{"https://waze.com/ul", "https://www.google.com/maps/dir", "https://maps.apple.com/"} {
			l, ok := links[h]
			if !ok {
				return fmt.Errorf("the day's end has no %s link", h)
			}
			if !strings.Contains(l, pt) && !strings.Contains(l, url.QueryEscape(pt)) {
				return fmt.Errorf("the %s link does not go to %s: %s", h, c.Name, l)
			}
		}
		return nil
	})
	sc.Step(`^the day's end links carry ([ABCD])'s name and no coordinate$`, func(w string) error {
		name := p.cps[strings.Index("ABCD", w)].Name
		links, err := n.dayEndLinks()
		if err != nil {
			return err
		}
		for h, l := range links {
			if !strings.Contains(l, url.QueryEscape(name)) && !strings.Contains(l, strings.ReplaceAll(url.QueryEscape(name), "+", "%20")) {
				return fmt.Errorf("the %s link does not carry %s: %s", h, name, l)
			}
			if coordRe.MatchString(l) {
				return fmt.Errorf("the %s link carries a coordinate for a private place: %s", h, l)
			}
		}
		return nil
	})
	sc.Step(`^the page says nothing about a stop on the way$`, func() error {
		if b := dayEndRe.FindString(n.page); strings.Contains(b, "first") {
			return fmt.Errorf("the day's end speaks of a stop on the way: %s", b)
		}
		return nil
	})
	sc.Step(`^kris has added the stop "([^"]*)" between B and C$`, func(name string) error {
		rv, err := p.sim.app.loadRide()
		if err != nil {
			return err
		}
		w := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "POST", "/driver/routing", url.Values{
			"leg": {rv.Legs[0].Slug}, "after": {strconv.Itoa(p.cps[1].Ord)}, "name": {name},
			"lat": {strconv.FormatFloat(stopLat(1)+0.25, 'f', -1, 64)}, "lng": {strconv.FormatFloat(stopsLng, 'f', -1, 64)},
		})
		if w.Code != http.StatusOK && w.Code != http.StatusSeeOther {
			return fmt.Errorf("adding the stop answered %d: %s", w.Code, w.Body.String())
		}
		return nil
	})
	sc.Step(`^the Waze and Apple Maps links go to "([^"]*)"$`, func(name string) error {
		links, err := n.dayEndLinks()
		if err != nil {
			return err
		}
		for _, h := range []string{"https://waze.com/ul", "https://maps.apple.com/"} {
			if !strings.Contains(links[h], url.QueryEscape(name)) {
				return fmt.Errorf("the %s link does not go to %s: %s", h, name, links[h])
			}
		}
		return nil
	})
	sc.Step(`^the Google Maps link goes to ([ABCD]) by way of "([^"]*)"$`, func(w, name string) error {
		c := p.cps[strings.Index("ABCD", w)]
		links, err := n.dayEndLinks()
		if err != nil {
			return err
		}
		g := links["https://www.google.com/maps/dir"]
		dest := strconv.FormatFloat(c.Lat, 'f', -1, 64) + "," + strconv.FormatFloat(c.Lng, 'f', -1, 64)
		via := strconv.FormatFloat(stopLat(1)+0.25, 'f', -1, 64) + "," + strconv.FormatFloat(stopsLng, 'f', -1, 64)
		if !strings.Contains(g, "destination="+url.QueryEscape(dest)) || !strings.Contains(g, "waypoints="+url.QueryEscape(via)) {
			return fmt.Errorf("the Google Maps link is not %s by way of %s: %s", c.Name, name, g)
		}
		return nil
	})
	sc.Step(`^the page says Waze and Apple Maps go to "([^"]*)" first and Google Maps carries it on the way$`, func(name string) error {
		b := html.UnescapeString(dayEndRe.FindString(n.page))
		if !strings.Contains(b, "Waze and Apple Maps go to "+name+" first") || !strings.Contains(b, "Google Maps carries it") {
			return fmt.Errorf("the day's end does not say how each navigator takes %s: %s", name, b)
		}
		return nil
	})
	sc.Step(`^the place search knows "([^"]*)"$`, func(name string) error {
		n.stubSearch(`{"results":[{"poi":{"name":"` + name + `"},"address":{"freeformAddress":"Ridge County, ZZ"},"position":{"lat":40.75,"lon":-100}}]}`)
		return nil
	})
	sc.Step(`^the place search knows nothing$`, func() error { n.stubSearch(`{"results":[]}`); return nil })
	sc.Step(`^kris looks up the stop "([^"]*)"$`, func(q string) error {
		return n.get("/?stop="+url.QueryEscape(q), true)
	})
	sc.Step(`^the page shows the found place "([^"]*)" with an Add form$`, func(name string) error {
		m := addFormRe.FindStringSubmatch(n.page)
		if m == nil {
			return fmt.Errorf("the page has no Add form posting to /driver/routing")
		}
		n.add = url.Values{}
		for _, h := range hiddenRe.FindAllStringSubmatch(m[1], -1) {
			n.add.Set(h[1], html.UnescapeString(h[2]))
		}
		if !strings.Contains(n.add.Get("name"), name) {
			return fmt.Errorf("the Add form is not for %s: %v", name, n.add)
		}
		for _, k := range []string{"leg", "after", "lat", "lng"} {
			if n.add.Get(k) == "" {
				return fmt.Errorf("the Add form lacks %s: %v", k, n.add)
			}
		}
		return nil
	})
	sc.Step(`^the place search was asked near the day's road, never at a private place$`, func() error {
		n.tt.mu.Lock()
		defer n.tt.mu.Unlock()
		if len(n.tt.geoReq) == 0 {
			return fmt.Errorf("the place search was not asked")
		}
		q := n.tt.geoReq[len(n.tt.geoReq)-1]
		lat, e1 := strconv.ParseFloat(q.Get("lat"), 64)
		lng, e2 := strconv.ParseFloat(q.Get("lon"), 64)
		if e1 != nil || e2 != nil {
			return fmt.Errorf("the place search was not told where the day is: %v", q)
		}
		// On the line A to D (one meridian), between the stops, and not at a stop.
		if lng != stopsLng || lat <= stopLat(0) || lat >= stopLat(3) {
			return fmt.Errorf("the place search was biased off the day's road: %v,%v", lat, lng)
		}
		for _, c := range p.cps {
			if c.Lat == lat && c.Lng == lng {
				return fmt.Errorf("the place search was biased at a stop, %s", c.Name)
			}
		}
		return nil
	})
	sc.Step(`^kris confirms the found place$`, func() error {
		w := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "POST", "/driver/routing", n.add)
		if w.Code != http.StatusOK && w.Code != http.StatusSeeOther {
			return fmt.Errorf("confirming answered %d: %s", w.Code, w.Body.String())
		}
		return nil
	})
	sc.Step(`^the page says no place was found$`, func() error {
		if !strings.Contains(n.page, "No place found") {
			return fmt.Errorf("the page does not say no place was found")
		}
		return nil
	})
	sc.Step(`^the day holds the routing point "([^"]*)"$`, func(name string) error {
		rps, err := n.routing()
		if err != nil {
			return err
		}
		for _, r := range rps {
			if strings.Contains(r.Name, name) {
				return nil
			}
		}
		return fmt.Errorf("the day holds no routing point %q: %v", name, rps)
	})
	sc.Step(`^the day holds no routing point$`, func() error {
		rps, err := n.routing()
		if err != nil {
			return err
		}
		if len(rps) != 0 {
			return fmt.Errorf("the day holds %d routing points", len(rps))
		}
		return nil
	})
	sc.Step(`^the trip still has 4 stops$`, func() error {
		rv, err := p.sim.app.loadRide()
		if err != nil {
			return err
		}
		if got := len(rideCheckpoints(rv.Legs)); got != 4 {
			return fmt.Errorf("the trip has %d stops", got)
		}
		return nil
	})
	// A, B and C have this step in the start-and-check-in worlds; D is read the same way.
	sc.Step(`^(D) is still not arrived at$`, func(w string) error {
		rv, err := p.sim.app.loadRide()
		if err != nil {
			return err
		}
		for _, c := range rideCheckpoints(rv.Legs) {
			if c.ID == n.id(w) && c.ArrivedAt != "" {
				return fmt.Errorf("%s is arrived at %s", c.Name, c.ArrivedAt)
			}
		}
		return nil
	})
	sc.Step(`^([ABCD]) is a private place$`, func(w string) error {
		_, err := p.sim.app.store.db.Exec(`UPDATE checkpoints SET precision='fuzzed' WHERE id=?`, n.id(w))
		return err
	})
	sc.Step(`^the page carries no map link$`, func() error {
		if navRe.MatchString(n.page) {
			return fmt.Errorf("the page carries a map link: %s", hrefRe.FindString(n.page))
		}
		return nil
	})
	sc.Step(`^the page carries no check-in link$`, func() error {
		if strings.Contains(n.page, "/driver/post?checkpoint=") {
			return fmt.Errorf("the page carries a check-in link")
		}
		return nil
	})
}

func (n *navWorld) id(w string) int64 { return n.p.cps[strings.Index("ABCD", w)].ID }

func (n *navWorld) get(path string, signedIn bool) error {
	var c *http.Cookie
	if signedIn {
		c = n.p.sim.cookies["kris"]
	}
	w := doAs(n.p.t, n.p.sim.app, c, "GET", path, nil)
	if w.Code != http.StatusOK {
		return fmt.Errorf("%s answered %d", path, w.Code)
	}
	n.page = w.Body.String()
	return nil
}

func (n *navWorld) row(w string) (string, error) {
	name := n.p.cps[strings.Index("ABCD", w)].Name
	for _, r := range rowRe.FindAllString(n.page, -1) {
		if strings.Contains(r, `<span class="cp-name">`+name+`</span>`) {
			return r, nil
		}
	}
	return "", fmt.Errorf("the page has no row for %s", name)
}

func (n *navWorld) rowHas(w, want string) error {
	r, err := n.row(w)
	if err != nil {
		return err
	}
	if !strings.Contains(r, want) {
		return fmt.Errorf("the row for %s does not offer %q: %s", w, want, r)
	}
	return nil
}

func (n *navWorld) offersCheckIn(w string) error {
	r, err := n.row(w)
	if err != nil {
		return err
	}
	want := regexp.MustCompile(`<a [^>]*href="/driver/post\?checkpoint=` + strconv.FormatInt(n.id(w), 10) + `"[^>]*>\s*Check in\s*</a>`)
	if !want.MatchString(r) {
		return fmt.Errorf("the row for %s has no Check in link to its own post form: %s", w, r)
	}
	return nil
}

// dayEndLinks maps each navigator's host prefix to the href the day's end
// block carries for it.
func (n *navWorld) dayEndLinks() (map[string]string, error) {
	b := dayEndRe.FindString(n.page)
	if b == "" {
		return nil, fmt.Errorf("the page has no day's end block")
	}
	out := map[string]string{}
	for _, m := range hrefRe.FindAllStringSubmatch(b, -1) {
		l := html.UnescapeString(m[1])
		for _, h := range []string{"https://waze.com/ul", "https://www.google.com/maps/dir", "https://maps.apple.com/"} {
			if strings.HasPrefix(l, h) {
				out[h] = l
			}
		}
	}
	return out, nil
}

func (n *navWorld) routing() ([]RoutingPoint, error) {
	rv, err := n.p.sim.app.loadRide()
	if err != nil {
		return nil, err
	}
	return rv.Legs[0].RoutingPoints, nil
}

// stubSearch points the place search at a TomTom stand-in answering with body.
func (n *navWorld) stubSearch(body string) {
	n.tt = newTT(n.p.t)
	n.tt.geoBody = body
	n.p.sim.app.free = newFreeSvc(ttKey)
	n.p.sim.app.free.ttBase = n.tt.srv.URL
}

// postThrough posts the photo the way the stop's check-in form does: the
// stop's own checkpoint_id and leg, and nothing chosen about the start.
func (n *navWorld) postThrough(w string) error {
	p := n.p
	c := p.cps[strings.Index("ABCD", w)]
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("leg_id", strconv.FormatInt(c.LegID, 10))
	_ = mw.WriteField("checkpoint_id", strconv.FormatInt(c.ID, 10))
	_ = mw.WriteField("kind", "photo")
	if p.photo != nil {
		fw, _ := mw.CreateFormFile("photo", "p.jpg")
		_, _ = fw.Write(p.photo)
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/driver/post", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(p.sim.cookies["kris"])
	rec := httptest.NewRecorder()
	p.sim.app.routes().ServeHTTP(rec, req)
	p.last = rec
	return nil
}
