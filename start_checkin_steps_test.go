package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/start_and_checkin.feature. They ride the post world (same
// app, same trip of three stops A, B and C, same fake Redis) and add what the
// feature needs: a photo with a GPS of its own, the phone's position, the
// simulated car, and a reader for the records the bus carries.
type scWorld struct {
	p      *postWorld
	phone  bool // the phone reports a position at the stop being checked in
	phoneI int
	gps    bool
}

func initStartCheckinScenarios(sc *godog.ScenarioContext, p *postWorld) {
	s := &scWorld{p: p}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = scWorld{p: p}
		return ctx, nil
	})
	sc.Step(`^the active trip has stops A, B and C in order, none arrived at$`, func() error {
		p.lenient = true
		return p.tripWith(false)
	})
	sc.Step(`^the form offers "Start the drive from ([ABC])"$`, s.offersStart)
	sc.Step(`^A is not offered as a check-in$`, s.aNotCheckin)
	sc.Step(`^B and C are not offered yet$`, s.bcNotOffered)
	sc.Step(`^kris starts the drive with no photo$`, func() error { p.photo = nil; return s.send(0, false, false) })
	sc.Step(`^kris starts the drive with that photo$`, func() error { return s.send(0, true, false) })
	sc.Step(`^A is still not arrived at$`, func() error { return s.notArrived(0) })
	sc.Step(`^B is still not arrived at$`, func() error { return s.notArrived(1) })
	sc.Step(`^a photo taken at (\d\d:\d\d) with its GPS at ([ABC])$`, func(hm, which string) error {
		c := p.cps[strings.Index("ABC", which)]
		p.photo = jpegGPSAt("2026:11:07 "+hm+":00", c.Lat, c.Lng)
		return nil
	})
	sc.Step(`^a photo taken at (\d\d:\d\d) with its GPS 60 km from A$`, func(hm string) error {
		c := p.cps[0]
		p.photo = jpegGPSAt("2026:11:07 "+hm+":00", c.Lat+0.54, c.Lng)
		return nil
	})
	sc.Step(`^a photo with no GPS of its own$`, func() error { p.photo = jpegTaken(""); return nil })
	sc.Step(`^the phone reports a position at B$`, func() error { s.phone, s.phoneI = true, 1; return nil })
	sc.Step(`^A is departed at (\d\d:\d\d) in A's zone$`, s.departedAt)
	sc.Step(`^A is departed$`, func() error { return s.arrived(0) })
	sc.Step(`^B is arrived at$`, func() error { return s.arrived(1) })
	sc.Step(`^the request is refused with a message naming ([ABC]) and the distance$`, s.refusedNaming)
	sc.Step(`^the request is refused with a message asking for a position$`, func() error {
		return s.refusedBody("No position to check in at")
	})
	sc.Step(`^the drive has started at A$`, func() error {
		return p.sim.app.store.SetArrived(p.cps[0].ID, time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC), nil)
	})
	sc.Step(`^the form does not offer to start the drive$`, func() error {
		if strings.Contains(p.body, "Start the drive") {
			return fmt.Errorf("the form still offers to start the drive")
		}
		return nil
	})
	sc.Step(`^the check-in choices are B and C$`, p.choicesBC)
	sc.Step(`^kris posts that photo checking in at B$`, func() error { return s.send(1, true, s.phone) })
	sc.Step(`^no post was saved$`, s.noPost)
	sc.Step(`^simulation is allowed$`, func() error { p.sim.app.simAllowed = true; return nil })
	sc.Step(`^simulation is not allowed$`, func() error { p.sim.app.simAllowed = false; return nil })
	sc.Step(`^kris's simulated car is at B$`, s.simAtB)
	sc.Step(`^kris's simulated car is running$`, func() error {
		if _, ok := p.sim.app.sim.State("kris"); !ok {
			return fmt.Errorf("no simulated car is running for kris")
		}
		return nil
	})
	sc.Step(`^the bus carries a drive_started record whose source is "([a-z]+)"(?: with no latitude and no longitude)?$`,
		func(src string) error { return s.bus("drive_started", -1, src) })
	sc.Step(`^the bus carries a checkin_verified record for B whose source is "([a-z]+)"(?: with no latitude and no longitude)?$`,
		func(src string) error { return s.bus("checkin_verified", 1, src) })
	sc.Step(`^the bus carries a checkin_refused record for B with no latitude and no longitude$`,
		func() error { return s.bus("checkin_refused", 1, "") })
}

// jpegGPSAt is a decodable JPEG whose EXIF holds a DateTimeOriginal and the
// given GPS point, built from the hand-written fixture with its rationals and
// hemisphere letters rewritten.
func jpegGPSAt(taken string, lat, lng float64) []byte {
	payload := exifAPP1WithGPS(1, taken)
	t := payload[len(exifPrefix):]
	const gpsIFD, latAt, lngAt = 68, 142, 166
	put := func(at int, v float64) {
		hs := int64(math.Round(math.Abs(v) * 360000)) // hundredths of an arc second
		for i, r := range [][2]uint32{{uint32(hs / 360000), 1}, {uint32(hs % 360000 / 6000), 1}, {uint32(hs % 6000), 100}} {
			binary.LittleEndian.PutUint32(t[at+i*8:], r[0])
			binary.LittleEndian.PutUint32(t[at+i*8+4:], r[1])
		}
	}
	put(latAt, lat)
	put(lngAt, lng)
	t[gpsIFD+10], t[gpsIFD+34] = 'N', 'E'
	if lat < 0 {
		t[gpsIFD+10] = 'S'
	}
	if lng < 0 {
		t[gpsIFD+34] = 'W'
	}
	base := jpegTaken("")
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	out := append([]byte{}, base[:2]...)
	out = append(out, seg...)
	out = append(out, payload...)
	return append(out, base[2:]...)
}

// send posts the form choosing stop i (0 = A) as the milestone, with or
// without the photo and the phone's position at that stop.
func (s *scWorld) send(i int, withPhoto, withPhone bool) error {
	p := s.p
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("leg_id", strconv.FormatInt(p.cps[0].LegID, 10))
	_ = mw.WriteField("kind", "photo")
	_ = mw.WriteField("milestone_id", strconv.FormatInt(p.cps[i].ID, 10))
	if withPhone {
		_ = mw.WriteField("lat", strconv.FormatFloat(p.cps[s.phoneI].Lat, 'f', 4, 64))
		_ = mw.WriteField("lng", strconv.FormatFloat(p.cps[s.phoneI].Lng, 'f', 4, 64))
	}
	if withPhoto && p.photo != nil {
		fw, _ := mw.CreateFormFile("photo", "p.jpg")
		_, _ = fw.Write(p.photo)
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/driver/post", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(p.sim.cookies["kris"])
	w := httptest.NewRecorder()
	p.sim.app.routes().ServeHTTP(w, req)
	p.last = w
	return nil
}

func (s *scWorld) offersStart(which string) error {
	want := "Start the drive from " + s.p.cps[strings.Index("ABC", which)].Name
	if !strings.Contains(s.p.body, want) {
		return fmt.Errorf("the form does not say %q", want)
	}
	return nil
}

func (s *scWorld) radioIDs() []string {
	var out []string
	for _, m := range radioRe.FindAllString(s.p.body, -1) {
		if i := strings.Index(m, `value="`); i >= 0 {
			v := m[i+7:]
			out = append(out, v[:strings.Index(v, `"`)])
		}
	}
	return out
}

func (s *scWorld) aNotCheckin() error {
	if strings.Contains(s.p.body, "Check in (optional)") {
		return fmt.Errorf("the form is already asking for check-ins")
	}
	return nil
}

func (s *scWorld) bcNotOffered() error {
	for _, v := range s.radioIDs() {
		for _, i := range []int{1, 2} {
			if v == strconv.FormatInt(s.p.cps[i].ID, 10) {
				return fmt.Errorf("%s is offered", s.p.cps[i].Name)
			}
		}
	}
	return nil
}

func (s *scWorld) notArrived(i int) error {
	if a := s.p.cp(i).ArrivedAt; a != "" {
		return fmt.Errorf("%s arrived at %s", s.p.cps[i].Name, a)
	}
	return nil
}

func (s *scWorld) arrived(i int) error {
	if s.p.cp(i).ArrivedAt == "" {
		return fmt.Errorf("%s has no arrival (answer %d %s)", s.p.cps[i].Name, s.p.last.Code, s.p.last.Body.String())
	}
	return nil
}

func (s *scWorld) departedAt(hm string) error {
	c := s.p.cp(0)
	at, ok := parseUTC(c.ArrivedAt)
	if !ok {
		return fmt.Errorf("A has no arrival: %q (answer %d %s)", c.ArrivedAt, s.p.last.Code, s.p.last.Body.String())
	}
	if got := at.In(loadLoc(c.TZ)).Format("2006-01-02 15:04"); got != "2026-11-07 "+hm {
		return fmt.Errorf("A departed %s in %s", got, c.TZ)
	}
	return nil
}

func (s *scWorld) refusedBody(contains string) error {
	l := s.p.last
	if l.Code != http.StatusBadRequest || !strings.Contains(l.Body.String(), contains) {
		return fmt.Errorf("answer %d, want a 400 saying %q: %s", l.Code, contains, l.Body.String())
	}
	return nil
}

func (s *scWorld) refusedNaming(which string) error {
	name := s.p.cps[strings.Index("ABC", which)].Name
	if err := s.refusedBody(name); err != nil {
		return err
	}
	if !strings.Contains(s.p.last.Body.String(), " km ") {
		return fmt.Errorf("the refusal does not give a distance: %s", s.p.last.Body.String())
	}
	return nil
}

func (s *scWorld) noPost() error {
	posts, err := s.p.sim.app.store.Posts("post-photo-trip", 0)
	if err != nil {
		return err
	}
	if len(posts) != 0 {
		return fmt.Errorf("%d posts were saved", len(posts))
	}
	return nil
}

// simAtB runs kris's simulated car and moves stop B to where it is now, so the
// car is at B: the fixture trip is in Nebraska, the corridor is in Ontario.
func (s *scWorld) simAtB() error {
	if err := s.p.sim.started("kris", simCorKGN, 7, 60); err != nil {
		return err
	}
	la, ln, _, _, _, ok := s.p.sim.app.sim.PositionAt("kris", s.p.sim.now)
	if !ok {
		return fmt.Errorf("the simulated car is not running")
	}
	if _, err := s.p.sim.app.store.db.Exec(`UPDATE checkpoints SET lat=?, lng=? WHERE id=?`, la, ln, s.p.cps[1].ID); err != nil {
		return err
	}
	rv, err := s.p.sim.app.loadRide()
	if err != nil {
		return err
	}
	s.p.cps = rideCheckpoints(rv.Legs)
	return nil
}

// bus waits for a record of the given event (for stop i, 0-based; -1 = any)
// on the fake Redis, checks its source and that it carries no coordinate.
func (s *scWorld) bus(event string, stop int, source string) error {
	f := s.p.sim.redis
	for i := 0; i < 200; i++ {
		f.mu.Lock()
		cmds := append([][]string(nil), f.cmds...)
		f.mu.Unlock()
		for _, c := range cmds {
			if len(c) < 3 || c[0] != "LPUSH" {
				continue
			}
			var rec map[string]any
			if json.Unmarshal([]byte(c[2]), &rec) != nil || rec["event"] != event {
				continue
			}
			if stop >= 0 && rec["stop"] != float64(s.p.cps[stop].Ord) {
				return fmt.Errorf("%s names stop %v, want %d", event, rec["stop"], s.p.cps[stop].Ord)
			}
			if source != "" && rec["source"] != source {
				return fmt.Errorf("%s says source %v, want %s", event, rec["source"], source)
			}
			s.p.sim.hudRec = rec
			return s.p.sim.meltHasNoCoordinates()
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("no %s record reached the bus", event)
}
