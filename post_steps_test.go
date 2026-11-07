package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// postWorld rides on the sim world (same app, signed-in cookies, fake Redis)
// and swaps the active trip for a three-milestone one of its own.
type postWorld struct {
	t     *testing.T
	sim   *simWorld
	cps   []Checkpoint // A, B, C in route order
	arrA  string
	photo []byte
	body  string
	last  *httptest.ResponseRecorder
	pred  time.Time
}

// rawPrivateLat/Lng is where the trip file puts milestone C. The fuzzed flag
// makes the store publish only its 0.05 degree cell.
const (
	rawPrivateLat = 42.0456
	rawPrivateLng = -102.0123
)

func initPostScenarios(t *testing.T, sc *godog.ScenarioContext, sim *simWorld) {
	p := &postWorld{t: t, sim: sim}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*p = postWorld{t: t, sim: sim}
		return ctx, nil
	})
	sc.Step(`^the active trip has milestones A, B and C in order, with A already arrived at$`, p.trip)
	sc.Step(`^kris opens the post form$`, p.open)
	sc.Step(`^its heading is "([^"]*)"$`, p.heading)
	sc.Step(`^no milestone is selected$`, p.noneSelected)
	sc.Step(`^the form carries no arrival button$`, p.noArrivalButton)
	sc.Step(`^the milestone choices are B and C$`, p.choicesBC)
	sc.Step(`^A is not among them$`, p.aNotAmong)
	sc.Step(`^kris posts a photo with no milestone chosen$`, func() error { return p.post("") })
	sc.Step(`^kris posts a photo with milestone ([ABC]) chosen$`, p.postWith)
	sc.Step(`^kris posts that photo with milestone ([ABC]) chosen$`, p.postWith)
	sc.Step(`^the post exists$`, p.postExists)
	sc.Step(`^B and C are still not arrived at$`, p.bcNotArrived)
	sc.Step(`^a photo taken at 10:42 in B's zone$`, func() error { p.photo = jpegTaken("2026:11:07 10:42:00"); return nil })
	sc.Step(`^a photo with no time of its own$`, func() error { p.photo = jpegTaken(""); return nil })
	sc.Step(`^B is arrived at 10:42 in B's zone$`, p.bArrived1042)
	sc.Step(`^C is arrived at the current time$`, p.cArrivedNow)
	sc.Step(`^the request is refused$`, p.refused)
	sc.Step(`^A's arrival time is unchanged$`, p.aUnchanged)
	sc.Step(`^an ETA prediction for B exists$`, p.seedPredictionB)
	sc.Step(`^that prediction is scored$`, p.scored)
	sc.Step(`^the bus carries an eta_hit record for B with no latitude and no longitude$`, p.busHit)
	sc.Step(`^each offered milestone carries its published position as data attributes$`, p.attrsPresent)
	sc.Step(`^a milestone that is a private place carries its snapped cell, never its point$`, p.privateSnapped)
}

// trip pushes a small trip (the fixture's first leg: Alder, Birch Gate, Cedar
// Camp) as the active one, makes C a private place, and stamps A arrived.
func (p *postWorld) trip() error {
	var doc map[string]any
	if err := json.Unmarshal(fixtureTrip(p.t), &doc); err != nil {
		return err
	}
	doc["ride"] = map[string]any{"slug": "post-photo-trip", "name": "Post a Photo", "start_date": "2030-05-01", "mode": "CAR"}
	delete(doc, "options")
	var keep []any
	for _, f := range doc["features"].([]any) {
		fm := f.(map[string]any)
		pr := fm["properties"].(map[string]any)
		if pr["leg"] != float64(1) || pr["kind"] == "candidate" || pr["choice"] == "valley" {
			continue
		}
		delete(pr, "option")
		delete(pr, "choice")
		if pr["name"] == "Cedar Camp" {
			pr["precision"] = "fuzzed"
			fm["geometry"].(map[string]any)["coordinates"] = []any{rawPrivateLng, rawPrivateLat}
		}
		keep = append(keep, f)
	}
	doc["features"] = keep
	raw, _ := json.Marshal(doc)
	if w := pushTrip(p.t, p.sim.app, raw, "?activate=1"); w.Code != http.StatusOK {
		return fmt.Errorf("push: %d %s", w.Code, w.Body.String())
	}
	rv, err := p.sim.app.loadRide()
	if err != nil {
		return err
	}
	p.cps = rideCheckpoints(rv.Legs)
	if len(p.cps) != 3 {
		return fmt.Errorf("want 3 milestones, the trip has %d", len(p.cps))
	}
	if err := p.sim.app.store.SetArrived(p.cps[0].ID, time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC), nil); err != nil {
		return err
	}
	p.arrA = p.cp(0).ArrivedAt
	return nil
}

func (p *postWorld) cp(i int) *Checkpoint {
	c, err := p.sim.app.store.Checkpoint(p.cps[i].ID)
	if err != nil {
		p.t.Fatal(err)
	}
	return c
}

func (p *postWorld) open() error {
	w := doAs(p.t, p.sim.app, p.sim.cookies["kris"], "GET", "/driver/post", nil)
	if w.Code != http.StatusOK {
		return fmt.Errorf("post form answered %d", w.Code)
	}
	p.body = w.Body.String()
	return nil
}

func (p *postWorld) heading(h string) error {
	if !strings.Contains(p.body, "<h1>"+h+"</h1>") {
		return fmt.Errorf("no <h1>%s</h1> in the form", h)
	}
	return nil
}

var radioRe = regexp.MustCompile(`<input[^>]*name="milestone_id"[^>]*>`)

func (p *postWorld) noneSelected() error {
	radios := radioRe.FindAllString(p.body, -1)
	if len(radios) == 0 {
		return fmt.Errorf("the form has no milestone radios")
	}
	for _, r := range radios {
		checked := strings.Contains(r, "checked")
		justAPhoto := strings.Contains(r, `value=""`)
		if checked != justAPhoto {
			return fmt.Errorf("selection is wrong at %s", r)
		}
	}
	return nil
}

func (p *postWorld) noArrivalButton() error {
	if strings.Contains(p.body, "/arrive") || strings.Contains(p.body, "Arrived:") || strings.Contains(p.body, "arrive-strip") {
		return fmt.Errorf("the form still carries an arrival button")
	}
	return nil
}

var choiceRe = regexp.MustCompile(`(?s)<label[^>]*data-lat="([^"]*)" data-lng="([^"]*)"[^>]*>\s*<input[^>]*name="milestone_id" value="(\d+)"[^>]*>\s*<span class="ms-name">([^<]*)</span>`)

func (p *postWorld) choices() []string {
	var out []string
	for _, m := range choiceRe.FindAllStringSubmatch(p.body, -1) {
		out = append(out, m[4])
	}
	return out
}

func (p *postWorld) choicesBC() error {
	got := strings.Join(p.choices(), ",")
	if want := p.cps[1].Name + "," + p.cps[2].Name; got != want {
		return fmt.Errorf("choices are %q, want %q", got, want)
	}
	return nil
}

func (p *postWorld) aNotAmong() error {
	for _, n := range p.choices() {
		if n == p.cps[0].Name {
			return fmt.Errorf("%s is offered", n)
		}
	}
	return nil
}

// jpegTaken is a tiny JPEG whose only EXIF is a DateTimeOriginal (none when
// taken is empty).
func jpegTaken(taken string) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 64)), nil); err != nil {
		panic(err)
	}
	b := buf.Bytes()
	if taken == "" {
		return b
	}
	payload := buildExifAPP1(exifKeep{orientation: 1, dateTime: taken})
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	out := append([]byte{}, b[:2]...)
	out = append(out, seg...)
	out = append(out, payload...)
	return append(out, b[2:]...)
}

func (p *postWorld) post(milestone string) error {
	if p.photo == nil {
		p.photo = jpegTaken("")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("leg_id", strconv.FormatInt(p.cps[0].LegID, 10))
	_ = mw.WriteField("kind", "photo")
	_ = mw.WriteField("milestone_id", milestone)
	fw, _ := mw.CreateFormFile("photo", "p.jpg")
	_, _ = fw.Write(p.photo)
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/driver/post", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(p.sim.cookies["kris"])
	w := httptest.NewRecorder()
	p.sim.app.routes().ServeHTTP(w, req)
	p.last = w
	return nil
}

func (p *postWorld) postWith(which string) error {
	return p.post(strconv.FormatInt(p.cps[strings.Index("ABC", which)].ID, 10))
}

func (p *postWorld) postExists() error {
	posts, err := p.sim.app.store.Posts("post-photo-trip", 0)
	if err != nil {
		return err
	}
	if len(posts) != 1 {
		return fmt.Errorf("want one post, have %d (last answer %d %s)", len(posts), p.last.Code, p.last.Body.String())
	}
	return nil
}

func (p *postWorld) bcNotArrived() error {
	for _, i := range []int{1, 2} {
		if a := p.cp(i).ArrivedAt; a != "" {
			return fmt.Errorf("%s arrived at %s", p.cps[i].Name, a)
		}
	}
	return nil
}

func (p *postWorld) bArrived1042() error {
	c := p.cp(1)
	at, ok := parseUTC(c.ArrivedAt)
	if !ok {
		return fmt.Errorf("B has no arrival: %q (answer %d %s)", c.ArrivedAt, p.last.Code, p.last.Body.String())
	}
	if got := at.In(loadLoc(c.TZ)).Format("2006-01-02 15:04"); got != "2026-11-07 10:42" {
		return fmt.Errorf("B arrived %s in %s", got, c.TZ)
	}
	return nil
}

func (p *postWorld) cArrivedNow() error {
	at, ok := parseUTC(p.cp(2).ArrivedAt)
	if !ok {
		return fmt.Errorf("C has no arrival (answer %d %s)", p.last.Code, p.last.Body.String())
	}
	if d := time.Since(at); d < -time.Minute || d > 2*time.Minute {
		return fmt.Errorf("C arrived %s ago", d)
	}
	return nil
}

func (p *postWorld) refused() error {
	if p.last.Code != http.StatusBadRequest || !strings.Contains(p.last.Body.String(), "milestone already reached or unknown") {
		return fmt.Errorf("answer %d: %s", p.last.Code, p.last.Body.String())
	}
	return nil
}

func (p *postWorld) aUnchanged() error {
	if a := p.cp(0).ArrivedAt; a != p.arrA {
		return fmt.Errorf("A arrival %q, was %q", a, p.arrA)
	}
	return nil
}

func (p *postWorld) seedPredictionB() error {
	p.pred = time.Now().Add(-2 * time.Hour).Truncate(time.Minute)
	_, err := p.sim.app.store.RecordETA("post-photo-trip", p.cps[1].ID, "kris", p.pred.Add(-time.Hour), p.pred, "plan")
	return err
}

func (p *postWorld) scored() error {
	rows, err := p.sim.app.store.ETALedger("post-photo-trip")
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0].ErrorMin == nil {
		return fmt.Errorf("ledger: %+v", rows)
	}
	return nil
}

func (p *postWorld) busHit() error {
	f := p.sim.redis
	for i := 0; i < 200; i++ {
		f.mu.Lock()
		cmds := append([][]string(nil), f.cmds...)
		f.mu.Unlock()
		for _, c := range cmds {
			if len(c) < 3 || c[0] != "LPUSH" {
				continue
			}
			var rec map[string]any
			if json.Unmarshal([]byte(c[2]), &rec) != nil || rec["event"] != "eta_hit" {
				continue
			}
			p.sim.hudRec = rec
			if rec["checkpoint"] != p.cps[1].Name {
				return fmt.Errorf("eta_hit names %v", rec["checkpoint"])
			}
			return p.sim.meltHasNoCoordinates()
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("no eta_hit record reached the bus")
}

func (p *postWorld) attrsPresent() error {
	ms := choiceRe.FindAllStringSubmatch(p.body, -1)
	if len(ms) != 2 {
		return fmt.Errorf("want 2 offered milestones with data-lat and data-lng, found %d", len(ms))
	}
	for _, m := range ms {
		if _, err := strconv.ParseFloat(m[1], 64); err != nil {
			return fmt.Errorf("data-lat %q", m[1])
		}
		if _, err := strconv.ParseFloat(m[2], 64); err != nil {
			return fmt.Errorf("data-lng %q", m[2])
		}
	}
	return nil
}

func (p *postWorld) privateSnapped() error {
	for _, m := range choiceRe.FindAllStringSubmatch(p.body, -1) {
		if m[4] != p.cps[2].Name {
			continue
		}
		lat, _ := strconv.ParseFloat(m[1], 64)
		lng, _ := strconv.ParseFloat(m[2], 64)
		if lat != snap(rawPrivateLat) || lng != snap(rawPrivateLng) {
			return fmt.Errorf("private milestone carries %v,%v, want the cell %v,%v", lat, lng, snap(rawPrivateLat), snap(rawPrivateLng))
		}
		if strings.Contains(p.body, "42.0456") || strings.Contains(p.body, "102.0123") {
			return fmt.Errorf("the raw point is in the page")
		}
		return nil
	}
	return fmt.Errorf("the private milestone is not offered")
}
