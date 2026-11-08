package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/fuzz_this.feature. They ride the start-and-check-in world
// (stops A to D on one meridian, 55 km apart; the GPS JPEG helper, the bus
// reader) and read what the site publishes about one post: its pin on the post
// card, in the route data and the census, and in what Chester is told.
//
// A "tap" goes through the page: the step finds the card's own form and posts
// it, so a button that is missing or wired to the wrong place fails here.
//
// The exact point of the "pinned exactly" posts has decimals nothing else on a
// page carries, so "contains the exact point" is the absence of those decimal
// strings.
type fuzzWorld struct {
	p *postWorld
	s *scWorld

	postID             int64   // the post under test
	exactLat, exactLng float64 // where it was pinned exactly, or the photo's own GPS
	phoneLat, phoneLng float64
	cx                 *chesterContext
}

const (
	fuzzExactLat = 40.2371
	fuzzExactLng = -99.7619
)

func initFuzzThisScenarios(sc *godog.ScenarioContext, p *postWorld, s *scWorld) {
	w := &fuzzWorld{p: p, s: s}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = fuzzWorld{p: p, s: s}
		return ctx, nil
	})
	sc.Step(`^the form offers "Fuzz This"$`, func() error {
		if !strings.Contains(p.body, "Fuzz This") {
			return fmt.Errorf("the post form does not offer Fuzz This")
		}
		return nil
	})
	sc.Step(`^a photo with its GPS 30 km from every stop$`, func() error {
		w.exactLat, w.exactLng = fuzzExactLat, fuzzExactLng
		p.photo = jpegGPSAt("2026:11:07 12:00:00", w.exactLat, w.exactLng)
		return nil
	})
	sc.Step(`^kris posts that photo with "Fuzz This" on$`, func() error { return w.postPhoto(true) })
	sc.Step(`^kris has posted that photo with "Fuzz This" on$`, func() error { return w.postPhoto(true) })
	sc.Step(`^the post's published location is fuzzy$`, w.fuzzy)
	sc.Step(`^the post's published location is not the photo's own GPS$`, func() error { return w.notExact() })
	sc.Step(`^the post's published location is the photo's own GPS$`, w.isExact)
	sc.Step(`^the post's published location is the exact point it had before$`, w.isExact)
	sc.Step(`^the post's published location is still the exact point$`, w.isExact)
	sc.Step(`^the page after posting offers "Fuzz This" for that post$`, w.noticeOffers)
	sc.Step(`^a post pinned exactly 30 km from every stop$`, w.pinnedExactly)
	sc.Step(`^kris taps "(Fuzz This|Unfuzz)" on that post$`, w.tap)
	sc.Step(`^kris has tapped "(Fuzz This|Unfuzz)" on that post$`, w.tap)
	sc.Step(`^the post card offers "(Fuzz This|Unfuzz)"$`, func(b string) error { return w.card(b, true) })
	sc.Step(`^that post offers no "(Fuzz This|Unfuzz)"$`, func(b string) error { return w.card(b, false) })
	sc.Step(`^the bus carries a post_fuzzed record with no latitude and no longitude$`, w.busFuzzed)
	sc.Step(`^the home page, the post's card, the route data and the posts census are read$`, w.readAll)
	sc.Step(`^none of them contains the exact point$`, w.noneHasExact)
	sc.Step(`^a visitor who is not signed in tries to fuzz that post$`, func() error {
		r := doAs(p.t, p.sim.app, nil, "POST", fmt.Sprintf("/driver/post/%d/fuzz", w.postID), url.Values{})
		p.last = r
		return nil
	})
	sc.Step(`^a visitor sees no "Fuzz This" on the post card$`, w.visitorSees)
	sc.Step(`^a post with no location$`, w.noLocation)
	sc.Step(`^kris opens the page with that post$`, func() error { return nil })
	sc.Step(`^kris re-pins that post to no pin$`, func() error {
		r := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "POST", fmt.Sprintf("/driver/post/%d/pin", w.postID),
			url.Values{"pin_source": {"none"}, "fuzz_choice": {"1"}})
		if r.Code != http.StatusSeeOther {
			return fmt.Errorf("re-pin answered %d: %s", r.Code, r.Body.String())
		}
		return nil
	})
	sc.Step(`^it carries the post's exact location$`, w.chesterExact)
	sc.Step(`^it carries the fuzzy location and not the exact point$`, w.chesterFuzzy)
	sc.Step(`^stop B is a private place$`, w.bPrivate)
	sc.Step(`^a post pinned exactly at B with fuzz turned off$`, w.pinnedAtBUnfuzzed)
	sc.Step(`^it carries B's fuzzed cell and not the exact point$`, w.chesterCell)
	sc.Step(`^the phone reports an exact position 30 km from every stop$`, func() error {
		w.phoneLat, w.phoneLng = fuzzExactLat, fuzzExactLng
		return nil
	})
	sc.Step(`^Chester's context is built from that position$`, func() error {
		cx, err := p.sim.app.chesterContext(p.sim.now, &w.phoneLat, &w.phoneLng)
		w.cx = cx
		return err
	})
	sc.Step(`^it carries the position rounded to three decimals$`, w.chesterRounded)
}

// --- posting -------------------------------------------------------------------------------

// postPhoto sends the form with the photo in p.photo and the fuzz choice made
// the way the page makes it: fuzz_choice says the driver's page carried the
// toggle, fuzz_location is the toggle.
func (w *fuzzWorld) postPhoto(fuzz bool) error {
	p := w.p
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("leg_id", strconv.FormatInt(p.cps[0].LegID, 10))
	_ = mw.WriteField("kind", "photo")
	_ = mw.WriteField("fuzz_choice", "1")
	if fuzz {
		_ = mw.WriteField("fuzz_location", "1")
	}
	fw, _ := mw.CreateFormFile("photo", "p.jpg")
	_, _ = fw.Write(p.photo)
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/driver/post", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(p.sim.cookies["kris"])
	rec := httptest.NewRecorder()
	p.sim.app.routes().ServeHTTP(rec, req)
	p.last = rec
	if rec.Code != http.StatusSeeOther {
		return fmt.Errorf("posting answered %d: %s", rec.Code, rec.Body.String())
	}
	return nil
}

// thePost is the one post of the ride, as the store publishes it.
func (w *fuzzWorld) thePost() (*Post, error) {
	posts, err := w.p.sim.app.store.Posts("post-photo-trip", 0)
	if err != nil {
		return nil, err
	}
	if len(posts) != 1 {
		return nil, fmt.Errorf("want one post, have %d (last answer %d %s)", len(posts), w.p.last.Code, w.p.last.Body.String())
	}
	w.postID = posts[0].ID
	return &posts[0], nil
}

// pinnedExactly puts a post straight into the store at the exact point.
func (w *fuzzWorld) pinnedExactly() error {
	w.exactLat, w.exactLng = fuzzExactLat, fuzzExactLng
	return w.insert(&w.exactLat, &w.exactLng)
}

func (w *fuzzWorld) noLocation() error { return w.insert(nil, nil) }

func (w *fuzzWorld) insert(lat, lng *float64) error {
	p := w.p
	post := &Post{LegID: p.cps[0].LegID, Kind: "note", Title: "a stop on the road",
		OccurredAt: utcString(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)), TZ: "America/Chicago",
		Lat: lat, Lng: lng, PinSource: pinNone}
	if lat != nil {
		post.PinSource = pinBrowser
	}
	if err := p.sim.app.store.CreatePost(post); err != nil {
		return err
	}
	w.postID = post.ID
	return nil
}

// --- what is published ---------------------------------------------------------------------

func onGrid(v float64) bool {
	q := v / 0.05
	return math.Abs(q-math.Round(q)) < 1e-6
}

// fuzzy: pin_fuzzed is set, the published point is on the 0.05 degree grid (or
// is a stop's own point) and is not the exact one.
func (w *fuzzWorld) fuzzy() error {
	post, err := w.thePost()
	if err != nil {
		return err
	}
	if !post.PinFuzzed || post.PinLat == nil || post.PinLng == nil {
		return fmt.Errorf("the post is not published fuzzed (pin_fuzzed %v, pin %v)", post.PinFuzzed, post.PinLat)
	}
	la, ln := *post.PinLat, *post.PinLng
	atStop := false
	for _, c := range w.p.cps {
		if math.Abs(c.Lat-la) < 1e-9 && math.Abs(c.Lng-ln) < 1e-9 {
			atStop = true
		}
	}
	if !atStop && !(onGrid(la) && onGrid(ln)) {
		return fmt.Errorf("the published point %v, %v is on neither the 0.05 degree grid nor a stop's cell", la, ln)
	}
	if w.same(la, ln) {
		return fmt.Errorf("the published point is the exact one")
	}
	return nil
}

func (w *fuzzWorld) same(la, ln float64) bool {
	return math.Abs(la-w.exactLat) < 1e-4 && math.Abs(ln-w.exactLng) < 1e-4
}

func (w *fuzzWorld) notExact() error {
	post, err := w.thePost()
	if err != nil {
		return err
	}
	if post.PinLat != nil && w.same(*post.PinLat, *post.PinLng) {
		return fmt.Errorf("the published point is the photo's own GPS")
	}
	return nil
}

func (w *fuzzWorld) isExact() error {
	post, err := w.thePost()
	if err != nil {
		return err
	}
	if post.PinFuzzed || post.PinLat == nil || post.PinLng == nil {
		return fmt.Errorf("the post is still fuzzed or has no pin (pin_fuzzed %v)", post.PinFuzzed)
	}
	if !w.same(*post.PinLat, *post.PinLng) {
		return fmt.Errorf("the published point is %v, %v, want %v, %v", *post.PinLat, *post.PinLng, w.exactLat, w.exactLng)
	}
	return nil
}

// --- the page ------------------------------------------------------------------------------

func (w *fuzzWorld) get(c *http.Cookie, path string) (string, error) {
	r := doAs(w.p.t, w.p.sim.app, c, "GET", path, nil)
	if r.Code != http.StatusOK {
		return "", fmt.Errorf("GET %s answered %d", path, r.Code)
	}
	return r.Body.String(), nil
}

// cardOf is the post's card on a page.
func (w *fuzzWorld) cardOf(page string) (string, error) {
	re := regexp.MustCompile(fmt.Sprintf(`(?s)<article[^>]*id="post-%d".*?</article>`, w.postID))
	if m := re.FindString(page); m != "" {
		return m, nil
	}
	return "", fmt.Errorf("no card for post %d on the page", w.postID)
}

// buttonForm is the form on a card that holds a button of that label, and its
// action.
func buttonForm(card, label string) (action string, ok bool) {
	for _, m := range regexp.MustCompile(`(?s)<form[^>]*action="([^"]*)"[^>]*>(.*?)</form>`).FindAllStringSubmatch(card, -1) {
		if regexp.MustCompile(`>\s*` + regexp.QuoteMeta(label) + `\s*</button>`).MatchString(m[2]) {
			return m[1], true
		}
	}
	return "", false
}

func (w *fuzzWorld) card(label string, want bool) error {
	page, err := w.get(w.p.sim.cookies["kris"], "/")
	if err != nil {
		return err
	}
	card, err := w.cardOf(page)
	if err != nil {
		return err
	}
	action, ok := buttonForm(card, label)
	if ok != want {
		return fmt.Errorf("the card offers %q: %v, want %v", label, ok, want)
	}
	if ok && action != fmt.Sprintf("/driver/post/%d/%s", w.postID, map[string]string{"Fuzz This": "fuzz", "Unfuzz": "unfuzz"}[label]) {
		return fmt.Errorf("the %q button posts to %s", label, action)
	}
	return nil
}

func (w *fuzzWorld) tap(label string) error {
	if w.postID == 0 {
		if _, err := w.thePost(); err != nil {
			return err
		}
	}
	page, err := w.get(w.p.sim.cookies["kris"], "/")
	if err != nil {
		return err
	}
	card, err := w.cardOf(page)
	if err != nil {
		return err
	}
	action, ok := buttonForm(card, label)
	if !ok {
		return fmt.Errorf("the card has no %q button", label)
	}
	r := doAs(w.p.t, w.p.sim.app, w.p.sim.cookies["kris"], "POST", action, url.Values{})
	w.p.last = r
	if r.Code != http.StatusSeeOther {
		return fmt.Errorf("%q answered %d: %s", label, r.Code, r.Body.String())
	}
	return nil
}

// noticeOffers follows the redirect after posting and looks for the button for
// the new post on the page it lands on.
func (w *fuzzWorld) noticeOffers() error {
	if _, err := w.thePost(); err != nil {
		return err
	}
	loc := w.p.last.Header().Get("Location")
	if i := strings.Index(loc, "#"); i >= 0 {
		loc = loc[:i]
	}
	page, err := w.get(w.p.sim.cookies["kris"], loc)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(fmt.Sprintf(`(?s)<form[^>]*action="/driver/post/%d/fuzz"[^>]*>.*?>\s*Fuzz This\s*</button>`, w.postID)).MatchString(page) {
		return fmt.Errorf("%s offers no Fuzz This for post %d", loc, w.postID)
	}
	return nil
}

func (w *fuzzWorld) visitorSees() error {
	page, err := w.get(nil, "/")
	if err != nil {
		return err
	}
	card, err := w.cardOf(page)
	if err != nil {
		return err
	}
	if strings.Contains(card, "Fuzz This") || strings.Contains(card, "/fuzz") || strings.Contains(card, "Unfuzz") {
		return fmt.Errorf("a visitor sees Fuzz This on the card")
	}
	return nil
}

// readAll fetches every page and file a reader could use to find a post's
// pin, as the signed-in driver, who can see the most.
func (w *fuzzWorld) readAll() error {
	var parts []string
	kris := w.p.sim.cookies["kris"]
	for _, path := range []string{"/", "/route.geojson", "/driver/posts.json",
		fmt.Sprintf("/driver/post/%d/edit", w.postID), "/driver", "/driver/post"} {
		b, err := w.get(kris, path)
		if err != nil {
			return err
		}
		parts = append(parts, path+"\n"+b)
	}
	post, err := w.p.sim.app.store.Post(w.postID)
	if err != nil {
		return err
	}
	b, err := w.get(kris, "/legs/"+post.LegSlug)
	if err != nil {
		return err
	}
	w.p.body = strings.Join(append(parts, b), "\n")
	return nil
}

func (w *fuzzWorld) noneHasExact() error {
	for _, s := range exactStrings(w.exactLat, w.exactLng) {
		if strings.Contains(w.p.body, s) {
			return fmt.Errorf("a page or file carries the exact point %s", s)
		}
	}
	return nil
}

func exactStrings(lat, lng float64) []string {
	return []string{strconv.FormatFloat(lat, 'f', -1, 64), strconv.FormatFloat(lng, 'f', -1, 64)}
}

func (w *fuzzWorld) busFuzzed() error {
	if err := w.s.bus("post_fuzzed", -1, "", ""); err != nil {
		return err
	}
	rec := w.p.sim.hudRec
	if rec["post"] != float64(w.postID) {
		return fmt.Errorf("post_fuzzed names post %v, want %d", rec["post"], w.postID)
	}
	return nil
}

// --- what Chester is told ------------------------------------------------------------------

func (w *fuzzWorld) build() (*chesterContext, error) {
	if w.postID == 0 {
		if _, err := w.thePost(); err != nil {
			return nil, err
		}
	}
	cx, err := w.p.sim.app.chesterContext(w.p.sim.now, nil, nil)
	if err != nil {
		return nil, err
	}
	if cx.Pos == nil {
		return nil, fmt.Errorf("Chester's context carries no position")
	}
	w.cx = cx
	return cx, nil
}

func (w *fuzzWorld) told(cx *chesterContext, s string) bool {
	return strings.Contains(cx.Volatile, s) || strings.Contains(cx.Stable, s)
}

func (w *fuzzWorld) chesterExact() error {
	cx, err := w.build()
	if err != nil {
		return err
	}
	if cx.Pos.Source != "post" || !w.same(cx.Pos.Lat, cx.Pos.Lng) || math.Abs(cx.Pos.Lat-w.exactLat) > 1e-9 {
		return fmt.Errorf("Chester's position is %+v, want the exact %v, %v", *cx.Pos, w.exactLat, w.exactLng)
	}
	for _, s := range exactStrings(w.exactLat, w.exactLng) {
		if !w.told(cx, s) {
			return fmt.Errorf("the text Chester is given does not carry %s:\n%s", s, cx.Volatile)
		}
	}
	return nil
}

func (w *fuzzWorld) chesterFuzzy() error {
	cx, err := w.build()
	if err != nil {
		return err
	}
	if !onGrid(cx.Pos.Lat) || !onGrid(cx.Pos.Lng) || w.same(cx.Pos.Lat, cx.Pos.Lng) {
		return fmt.Errorf("Chester's position %+v is not the fuzzy one", *cx.Pos)
	}
	for _, s := range exactStrings(w.exactLat, w.exactLng) {
		if w.told(cx, s) {
			return fmt.Errorf("the text Chester is given carries the exact %s", s)
		}
	}
	return nil
}

const (
	bRawLat = 40.5123
	bRawLng = -100.0178
)

// bPrivate moves B off the grid and marks it a private place; the store then
// publishes only its 0.05 degree cell.
func (w *fuzzWorld) bPrivate() error {
	if err := w.s.moveStop(1, bRawLat, bRawLng); err != nil {
		return err
	}
	if _, err := w.p.sim.app.store.db.Exec(`UPDATE checkpoints SET precision='fuzzed' WHERE id=?`, w.p.cps[1].ID); err != nil {
		return err
	}
	return w.s.reload(len(w.p.cps))
}

func (w *fuzzWorld) pinnedAtBUnfuzzed() error {
	w.exactLat, w.exactLng = bRawLat, bRawLng
	w.p.photo = jpegGPSAt("2026:11:07 12:00:00", w.exactLat, w.exactLng)
	if err := w.postPhoto(false); err != nil {
		return err
	}
	post, err := w.thePost()
	if err != nil {
		return err
	}
	if post.PinFuzzed || post.Lat == nil || math.Abs(*post.Lat-bRawLat) > 1e-4 {
		return fmt.Errorf("the post is not pinned exactly at B (fuzzed %v)", post.PinFuzzed)
	}
	return nil
}

func (w *fuzzWorld) chesterCell() error {
	cx, err := w.build()
	if err != nil {
		return err
	}
	if cx.Pos.Lat != w.p.cps[1].Lat || cx.Pos.Lng != w.p.cps[1].Lng || !cx.Pos.Snapped {
		return fmt.Errorf("Chester's position %+v is not B's cell %v, %v", *cx.Pos, w.p.cps[1].Lat, w.p.cps[1].Lng)
	}
	for _, s := range []string{"40.512", "100.017", "100.018"} {
		if w.told(cx, s) {
			return fmt.Errorf("the text Chester is given carries the exact point (%s)", s)
		}
	}
	return nil
}

func (w *fuzzWorld) chesterRounded() error {
	cx := w.cx
	if cx == nil || cx.Pos == nil {
		return fmt.Errorf("no position in Chester's context")
	}
	if cx.Pos.Source != "browser" || cx.Pos.Lat != 40.237 || cx.Pos.Lng != -99.762 {
		return fmt.Errorf("Chester's position is %+v, want 40.237, -99.762", *cx.Pos)
	}
	if w.told(cx, "40.2371") || w.told(cx, "99.7619") {
		return fmt.Errorf("the text Chester is given carries the exact point")
	}
	return nil
}
