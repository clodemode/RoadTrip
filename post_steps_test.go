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
	sc.Step(`^kris posts a photo with no milestone chosen$`, func() error { return p.post("") })
	sc.Step(`^kris posts a photo taken at ([ABC])$`, p.postWith)
	sc.Step(`^the post exists$`, p.postExists)
	sc.Step(`^B and C are still not arrived at$`, p.bcNotArrived)
	sc.Step(`^an ETA prediction for B exists$`, p.seedPredictionB)
	sc.Step(`^that prediction is scored$`, p.scored)
	sc.Step(`^the bus carries an eta_hit record for B with no latitude and no longitude$`, p.busHit)
	s := initStartCheckinScenarios(sc, p)
	initPinSkipScenarios(sc, p, s)
	initDoneStopsScenarios(sc, p)
	initFuzzThisScenarios(sc, p, s)
	initSimStepsScenarios(sc, p, s)
	initPostFormByTypeScenarios(sc, p)
	initTracksTopScenarios(p.t, sc, p)
}

// trip pushes a small trip (the fixture's first leg: Alder, Birch Gate, Cedar
// Camp) as the active one, makes C a private place, and stamps A arrived.
func (p *postWorld) trip() error { return p.tripWith(true) }

// tripWith is trip with A's arrival optional: the start-and-check-in feature
// begins before the drive has started.
func (p *postWorld) tripWith(arriveA bool) error {
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
	if arriveA {
		if err := p.sim.app.store.SetArrived(p.cps[0].ID, time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC), nil); err != nil {
			return err
		}
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
	for _, r := range radioRe.FindAllString(p.body, -1) {
		if strings.Contains(r, "checked") && !strings.Contains(r, `value=""`) {
			return fmt.Errorf("a stop is selected: %s", r)
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
	// A check-in needs a position: the phone is at the stop it names.
	for _, c := range p.cps {
		if milestone != "" && strconv.FormatInt(c.ID, 10) == milestone {
			_ = mw.WriteField("lat", strconv.FormatFloat(c.Lat, 'f', 4, 64))
			_ = mw.WriteField("lng", strconv.FormatFloat(c.Lng, 'f', 4, 64))
		}
	}
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
