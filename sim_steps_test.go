package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// simWorld is one scenario's world: an app, a clock the scenario owns, the
// cookies of the devices it signed in, and the last response.
type simWorld struct {
	t       *testing.T
	app     *App
	redis   *fakeRedis
	stub    *apiStub
	now     time.Time
	x       float64
	cookies map[string]*http.Cookie
	last    *httptest.ResponseRecorder
	seen    int // LPUSH records already read from the fake Redis
	hudRec  map[string]any
	posA    simPos
	posB    simPos
}

type simPos struct{ lat, lng, miles float64 }

func initSimScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &simWorld{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = simWorld{t: t, x: 60, cookies: map[string]*http.Cookie{},
			now: time.Date(2026, 11, 7, 15, 0, 0, 0, time.UTC)}
		w.stub = newAPIStub(t, quickScript())
		w.app = chesterApp(t, w.stub)
		w.redis = newFakeRedis(t, true)
		w.app.melt = newMelt(w.redis.addr(), "kate")
		w.app.sim = newSimSvc()
		w.app.sim.now = func() time.Time { return w.now }
		return ctx, nil
	})
	sc.Step(`^the site allows simulated drives$`, func() { w.app.simAllowed = true })
	sc.Step(`^the site does not allow simulated drives$`, func() { w.app.simAllowed = false })
	sc.Step(`^(\w+) is signed in$`, w.signIn)
	sc.Step(`^(\w+) starts a simulated drive on corridor "([^"]*)" with seed (\d+) and compression (\d+)$`, w.start)
	sc.Step(`^(\w+) started a simulated drive on corridor "([^"]*)" with seed (\d+) and compression (\d+)$`, w.started)
	sc.Step(`^the HUD asked with no browser position reports source "([^"]*)"$`, w.hudReports)
	sc.Step(`^the simulated position lies on the corridor between Kingston and Toronto$`, w.onCorridor)
	sc.Step(`^a second device signed in as (\w+) asks for the simulated position at the same instant$`, w.secondDevice)
	sc.Step(`^both devices see the same position within 1 metre$`, w.sameWithinAMetre)
	sc.Step(`^two simulated drives on corridor "([^"]*)" with seed (\d+) started at the same instant$`, w.twoDrives)
	sc.Step(`^their positions (\d+) simulated seconds later are identical$`, w.identicalLater)
	sc.Step(`^a drive with seed (\d+) started at that instant is somewhere else$`, w.otherSeed)
	sc.Step(`^the position (\d+) simulated seconds later is further along the corridor than at the start$`, w.furtherAlong)
	sc.Step(`^(\w+) asks Chester a question with no browser position$`, w.asksChester)
	sc.Step(`^Chester's context carries a position$`, w.chesterHasPosition)
	sc.Step(`^that position is the simulated one, coarsened the same way a phone position is$`, w.chesterPositionCoarsened)
	sc.Step(`^(\w+) stops the simulated drive$`, w.stop)
	sc.Step(`^asking for the simulated position answers that none is running$`, w.noneRunning)
	sc.Step(`^the request is refused with (\d+)$`, w.refusedWith)
	sc.Step(`^the HUD is asked with no browser position$`, w.hudAsked)
	sc.Step(`^the MELT record for that HUD serve says sim true$`, w.meltSaysSim)
	sc.Step(`^it carries no latitude and no longitude$`, w.meltHasNoCoordinates)
}

func (w *simWorld) signIn(name string) { w.cookies[name] = asUser(w.t, w.app, name, roleAdmin) }

func (w *simWorld) start(name, corridor string, seed, x int) error {
	if _, ok := w.cookies[name]; !ok {
		w.signIn(name)
	}
	w.x = float64(x)
	w.last = doAs(w.t, w.app, w.cookies[name], "POST", "/driver/sim/start", url.Values{
		"corridor": {corridor}, "seed": {strconv.Itoa(seed)}, "x": {strconv.Itoa(x)}})
	return nil
}

func (w *simWorld) started(name, corridor string, seed, x int) error {
	w.start(name, corridor, seed, x)
	if w.last.Code != http.StatusSeeOther {
		return fmt.Errorf("start answered %d: %s", w.last.Code, w.last.Body.String())
	}
	return nil
}

// hud asks /driver/hud.json with no position and reads the hud_served record
// it published to the fake Redis.
func (w *simWorld) hud(name string) error {
	w.last = doAs(w.t, w.app, w.cookies[name], "GET", "/driver/hud.json", nil)
	if w.last.Code != http.StatusOK {
		return fmt.Errorf("hud.json answered %d: %s", w.last.Code, w.last.Body.String())
	}
	cmds := w.redis.wait(w.t, w.seen+2)
	for _, c := range cmds[w.seen:] {
		if c[0] != "LPUSH" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(c[2]), &rec); err != nil {
			return err
		}
		if rec["event"] == "hud_served" {
			w.hudRec = rec
		}
	}
	w.seen += 2
	if w.hudRec == nil {
		return fmt.Errorf("no hud_served record was published")
	}
	return nil
}

func (w *simWorld) hudReports(source string) error {
	if err := w.hud("kris"); err != nil {
		return err
	}
	if w.hudRec["pos_source"] != source {
		return fmt.Errorf("hud_served pos_source is %v, want %q", w.hudRec["pos_source"], source)
	}
	return nil
}

func (w *simWorld) hudAsked() error { return w.hud("kris") }

// pos is what the server holds for whoever holds this cookie: the request is
// resolved to its user exactly as a handler does.
func (w *simWorld) pos(c *http.Cookie, at time.Time) (simPos, bool) {
	req := httptest.NewRequest("GET", "/driver", nil)
	req.AddCookie(c)
	lat, lng, _, _, mi, ok := w.app.sim.PositionAt(w.app.userOf(req), at)
	return simPos{lat, lng, mi}, ok
}

func (w *simWorld) later(simSecs int) time.Time {
	return w.now.Add(time.Duration(float64(simSecs) / w.x * float64(time.Second)))
}

func (w *simWorld) onCorridor() error {
	p, ok := w.pos(w.cookies["kris"], w.now)
	if !ok {
		return fmt.Errorf("no simulated position is running")
	}
	if p.lat < 43.60 || p.lat > 44.30 || p.lng < -79.40 || p.lng > -76.45 {
		return fmt.Errorf("%.4f, %.4f is not between Kingston and Toronto", p.lat, p.lng)
	}
	return nil
}

func (w *simWorld) secondDevice(name string) error {
	w.posA, _ = w.pos(w.cookies[name], w.now)
	second := asUser(w.t, w.app, name, roleAdmin)
	w.posB, _ = w.pos(second, w.now)
	return nil
}

func metres(a, b simPos) float64 { return haversineKm(a.lat, a.lng, b.lat, b.lng) * 1000 }

func (w *simWorld) sameWithinAMetre() error {
	if w.posA.lat == 0 || w.posB.lat == 0 {
		return fmt.Errorf("a device saw no position: %+v %+v", w.posA, w.posB)
	}
	if d := metres(w.posA, w.posB); d > 1 {
		return fmt.Errorf("the devices are %.1f m apart", d)
	}
	return nil
}

func (w *simWorld) twoDrives(corridor string, seed int) error {
	if err := w.started("kris", corridor, seed, 60); err != nil {
		return err
	}
	w.signIn("twin")
	w.last = doAs(w.t, w.app, w.cookies["twin"], "POST", "/driver/sim/start", url.Values{
		"corridor": {corridor}, "seed": {strconv.Itoa(seed)}, "x": {"60"}})
	if w.last.Code != http.StatusSeeOther {
		return fmt.Errorf("twin start answered %d", w.last.Code)
	}
	return nil
}

func (w *simWorld) identicalLater(secs int) error {
	a, _ := w.pos(w.cookies["kris"], w.later(secs))
	b, _ := w.pos(w.cookies["twin"], w.later(secs))
	if a.lat == 0 || a != b {
		return fmt.Errorf("the same seed drove two ways: %+v vs %+v", a, b)
	}
	return nil
}

func (w *simWorld) otherSeed(seed int) error {
	w.signIn("other")
	w.last = doAs(w.t, w.app, w.cookies["other"], "POST", "/driver/sim/start", url.Values{
		"corridor": {"kgn-yyz"}, "seed": {strconv.Itoa(seed)}, "x": {"60"}})
	a, _ := w.pos(w.cookies["kris"], w.later(120))
	o, ok := w.pos(w.cookies["other"], w.later(120))
	if !ok || metres(a, o) < 10 {
		return fmt.Errorf("seed %d is not somewhere else: %+v vs %+v (running %v)", seed, a, o, ok)
	}
	return nil
}

func (w *simWorld) furtherAlong(secs int) error {
	a, ok := w.pos(w.cookies["kris"], w.now)
	b, _ := w.pos(w.cookies["kris"], w.later(secs))
	if !ok || !(b.miles > a.miles+1) {
		return fmt.Errorf("miles done %.2f then %.2f (running %v)", a.miles, b.miles, ok)
	}
	return nil
}

func (w *simWorld) asksChester(name string) error {
	w.last = doAs(w.t, w.app, w.cookies[name], "POST", "/chester/ask", url.Values{"preset": {"fuel"}})
	if w.last.Code != http.StatusOK {
		return fmt.Errorf("ask answered %d: %s", w.last.Code, w.last.Body.String())
	}
	return nil
}

func (w *simWorld) chesterBody() (string, error) {
	if len(w.stub.requests()) == 0 {
		return "", fmt.Errorf("the API was never asked")
	}
	return string(w.stub.requests()[0].Body), nil
}

func (w *simWorld) chesterHasPosition() error {
	b, err := w.chesterBody()
	if err != nil {
		return err
	}
	if !strings.Contains(b, "POSITION: ") || strings.Contains(b, "POSITION: unknown") {
		return fmt.Errorf("Chester was told no position")
	}
	return nil
}

func (w *simWorld) chesterPositionCoarsened() error {
	b, err := w.chesterBody()
	if err != nil {
		return err
	}
	p, ok := w.pos(w.cookies["kris"], w.now)
	if !ok {
		return fmt.Errorf("no simulated position is running")
	}
	rv, err := w.app.loadRide()
	if err != nil {
		return err
	}
	pub, _ := publishPosition(p.lat, p.lng, rideCheckpoints(rv.Legs))
	want := fmt.Sprintf("POSITION: %s, %s", num3(pub.Lat), num3(pub.Lng))
	if !strings.Contains(b, want) {
		return fmt.Errorf("Chester was not told %q", want)
	}
	if strings.Contains(b, strconv.FormatFloat(p.lat, 'f', 4, 64)) {
		return fmt.Errorf("the finer position reached the API")
	}
	return nil
}

func (w *simWorld) stop(name string) error {
	w.last = doAs(w.t, w.app, w.cookies[name], "POST", "/driver/sim/stop", url.Values{})
	if w.last.Code != http.StatusSeeOther {
		return fmt.Errorf("stop answered %d", w.last.Code)
	}
	return nil
}

func (w *simWorld) noneRunning() error {
	if _, ok := w.pos(w.cookies["kris"], w.now); ok {
		return fmt.Errorf("a simulated position is still held")
	}
	r := doAs(w.t, w.app, w.cookies["kris"], "GET", "/driver/sim.json", nil)
	var j map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &j); err != nil || j["running"] != false {
		return fmt.Errorf("sim.json says %s", r.Body.String())
	}
	return nil
}

func (w *simWorld) refusedWith(code int) error {
	if w.last.Code != code {
		return fmt.Errorf("answered %d, want %d", w.last.Code, code)
	}
	return nil
}

func (w *simWorld) meltSaysSim() error {
	if w.hudRec["sim"] != true {
		return fmt.Errorf("the record says sim %v", w.hudRec["sim"])
	}
	return nil
}

func (w *simWorld) meltHasNoCoordinates() error {
	for k, v := range w.hudRec {
		kl := strings.ToLower(k)
		if kl == "lat" || kl == "lng" || kl == "lon" || strings.HasPrefix(kl, "latitude") || strings.HasPrefix(kl, "longitude") {
			return fmt.Errorf("the record carries %s", k)
		}
		if f, ok := v.(float64); ok && (math.Abs(f-44) < 1 && k != "miles_left") && k != "ts" {
			return fmt.Errorf("the record carries a coordinate-like value %s=%v", k, v)
		}
	}
	return nil
}
