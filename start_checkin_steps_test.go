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
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/start_and_checkin.feature. They ride the post world (same
// app, same fake Redis) and swap its trip for one of four stops, A to D, laid
// along a line of latitude 55 km apart. They add what the feature needs: a
// photo with a GPS of its own, the phone's position, the simulated car, and a
// reader for the records the bus carries.
type scWorld struct {
	p     *postWorld
	phone bool // the phone reports a position at stop phoneI
}

const stopsLng = -100.0

// stopLat is the latitude of stop i (0 = A): 0.5 degree, about 55 km, apart.
func stopLat(i int) float64 { return 40.0 + 0.5*float64(i) }

func initStartCheckinScenarios(sc *godog.ScenarioContext, p *postWorld) {
	s := &scWorld{p: p}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = scWorld{p: p}
		return ctx, nil
	})
	sc.Step(`^the active trip has stops A, B, C and D in order along a line, none arrived at$`, func() error { return s.line() })
	sc.Step(`^the active trip has stops A, B, C and D where B lies 40 km off the line from A to C$`, s.bOffLine)
	sc.Step(`^the form offers "Start the drive from ([ABCD])"$`, s.offersStart)
	sc.Step(`^the form offers no stop to choose$`, s.noStopToChoose)
	sc.Step(`^the form says the next stop is ([ABCD])$`, s.nextIs)
	sc.Step(`^kris starts the drive with no photo$`, func() error { p.photo = nil; return s.send(0, false, false) })
	sc.Step(`^kris starts the drive with that photo$`, func() error { return s.send(0, true, false) })
	sc.Step(`^kris posts that photo$`, func() error { return s.send(-1, true, s.phone) })
	sc.Step(`^the request is refused$`, func() error { return s.refusedBody("") })
	sc.Step(`^A is still not arrived at$`, func() error { return s.notArrived(0) })
	sc.Step(`^B is still not arrived at$`, func() error { return s.notArrived(1) })
	sc.Step(`^a photo taken at (\d\d:\d\d) with its GPS at ([ABCD])$`, func(hm, which string) error {
		i := strings.Index("ABCD", which)
		p.photo = jpegGPSAt("2026:11:07 "+hm+":00", p.cps[i].Lat, p.cps[i].Lng)
		return nil
	})
	sc.Step(`^a photo taken at (\d\d:\d\d) with its GPS 60 km from A$`, func(hm string) error {
		p.photo = jpegGPSAt("2026:11:07 "+hm+":00", p.cps[0].Lat+0.54, p.cps[0].Lng)
		return nil
	})
	// Between A and B, 20 km east of the line: at least 30 km from every stop.
	sc.Step(`^a photo taken at (\d\d:\d\d) with its GPS 30 km from every stop$`, func(hm string) error {
		p.photo = jpegGPSAt("2026:11:07 "+hm+":00", 40.25, stopsLng+0.24)
		return nil
	})
	// Far north of the line: more than 900 km from every stop.
	sc.Step(`^a photo taken at (\d\d:\d\d) with its GPS 900 km from every stop$`, func(hm string) error {
		p.photo = jpegGPSAt("2026:11:07 "+hm+":00", 50.0, stopsLng)
		return nil
	})
	sc.Step(`^a photo with no GPS of its own$`, func() error { p.photo = jpegTaken(""); return nil })
	sc.Step(`^the phone reports a position at B$`, func() error { s.phone = true; return nil })
	sc.Step(`^A is departed at (\d\d:\d\d) in A's zone$`, func(hm string) error { return s.arrivedAt(0, hm) })
	sc.Step(`^([ABCD]) is arrived at (\d\d:\d\d) in [ABCD]'s zone$`, func(w, hm string) error { return s.arrivedAt(strings.Index("ABCD", w), hm) })
	sc.Step(`^A is departed$`, func() error { return s.arrived(0) })
	// Given before the first request, Then after it.
	sc.Step(`^([ABCD]) is arrived at$`, func(w string) error {
		i := strings.Index("ABCD", w)
		if p.last == nil {
			return p.sim.app.store.SetArrived(p.cps[i].ID, time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC), nil)
		}
		return s.arrived(i)
	})
	sc.Step(`^([ABCD]) is skipped$`, func(w string) error {
		return p.sim.app.store.SetSkipped(p.cps[strings.Index("ABCD", w)].ID, time.Date(2026, 11, 6, 9, 30, 0, 0, time.UTC), "missed")
	})
	sc.Step(`^([ABCD]) is skipped as "([a-z]+)" and not arrived at$`, func(w, why string) error {
		return s.skipped(strings.Index("ABCD", w), why)
	})
	sc.Step(`^B is still skipped and not arrived at$`, func() error { return s.skipped(1, "missed") })
	sc.Step(`^the request is refused with a message naming ([ABC]) and the distance$`, s.refusedNaming)
	sc.Step(`^the drive has started at A$`, func() error {
		return p.sim.app.store.SetArrived(p.cps[0].ID, time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC), nil)
	})
	sc.Step(`^the form does not offer to start the drive$`, func() error {
		if strings.Contains(p.body, "Start the drive") {
			return fmt.Errorf("the form still offers to start the drive")
		}
		return nil
	})
	sc.Step(`^the notice says it was posted as a photo and names B as next$`, func() error {
		return s.noticeHas("Posted as a photo; next stop " + p.cps[1].Name)
	})
	sc.Step(`^the notice says C was reached and B was missed$`, func() error {
		return s.noticeHas("Reached "+p.cps[2].Name, "missed "+p.cps[1].Name)
	})
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
		func(src string) error { return s.bus("drive_started", -1, "source", src) })
	sc.Step(`^the bus carries a checkin_verified record for B whose source is "([a-z]+)"(?: with no latitude and no longitude)?$`,
		func(src string) error { return s.bus("checkin_verified", 1, "source", src) })
	sc.Step(`^the bus carries a checkin_skipped record for B whose reason is "([a-z]+)"(?: with no latitude and no longitude)?$`,
		func(why string) error { return s.bus("checkin_skipped", 1, "reason", why) })
	sc.Step(`^the bus carries a checkin_skipped record for B with no latitude and no longitude$`,
		func() error { return s.bus("checkin_skipped", 1, "", "") })
}

// line pushes the trip of four stops (the fixture's first leg with a fourth
// stop added) as the active one, laid on one meridian.
func (s *scWorld) line() error {
	p := s.p
	var doc map[string]any
	if err := json.Unmarshal(fixtureTrip(p.t), &doc); err != nil {
		return err
	}
	doc["ride"] = map[string]any{"slug": "post-photo-trip", "name": "Start and Check In", "start_date": "2030-05-01", "mode": "CAR"}
	delete(doc, "options")
	var keep []any
	var last map[string]any
	for _, f := range doc["features"].([]any) {
		fm := f.(map[string]any)
		pr := fm["properties"].(map[string]any)
		if pr["leg"] != float64(1) || pr["kind"] == "candidate" || pr["choice"] == "valley" {
			continue
		}
		delete(pr, "option")
		delete(pr, "choice")
		if g := fm["geometry"].(map[string]any); g["type"] == "LineString" {
			var pts []any
			for i := 0; i < 4; i++ {
				pts = append(pts, []any{stopsLng, stopLat(i)})
			}
			g["coordinates"] = pts
		}
		if pr["kind"] == "checkpoint" {
			i := int(pr["order"].(float64)) - 1
			pr["precision"] = "approx"
			delete(pr, "is_terminal")
			fm["geometry"].(map[string]any)["coordinates"] = []any{stopsLng, stopLat(i)}
			last = fm
		}
		keep = append(keep, f)
	}
	raw, _ := json.Marshal(last)
	var d map[string]any
	_ = json.Unmarshal(raw, &d)
	dp := d["properties"].(map[string]any)
	dp["order"], dp["name"], dp["label"], dp["is_terminal"] = 4, "Dover Dock", "Night 2", true
	d["geometry"].(map[string]any)["coordinates"] = []any{stopsLng, stopLat(3)}
	doc["features"] = append(keep, d)
	out, _ := json.Marshal(doc)
	if w := pushTrip(p.t, p.sim.app, out, "?activate=1"); w.Code != http.StatusOK {
		return fmt.Errorf("push: %d %s", w.Code, w.Body.String())
	}
	return s.reload(4)
}

func (s *scWorld) reload(want int) error {
	rv, err := s.p.sim.app.loadRide()
	if err != nil {
		return err
	}
	s.p.cps = rideCheckpoints(rv.Legs)
	if len(s.p.cps) != want {
		return fmt.Errorf("want %d stops, the trip has %d", want, len(s.p.cps))
	}
	return nil
}

// moveStop puts stop i at a point, as the plan would have it.
func (s *scWorld) moveStop(i int, lat, lng float64) error {
	if _, err := s.p.sim.app.store.db.Exec(`UPDATE checkpoints SET lat=?, lng=? WHERE id=?`, lat, lng, s.p.cps[i].ID); err != nil {
		return err
	}
	return s.reload(len(s.p.cps))
}

// bOffLine moves B 40 km east of the line from A to C.
func (s *scWorld) bOffLine() error {
	dLng := 40 / (111.195 * math.Cos(stopLat(1)*math.Pi/180))
	return s.moveStop(1, stopLat(1), stopsLng+dLng)
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

// send posts the form. i is the stop chosen as the milestone (0 = A, the only
// choice the form ever offers; -1 = none), with or without the photo and the
// phone's position at B.
func (s *scWorld) send(i int, withPhoto, withPhone bool) error {
	p := s.p
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("leg_id", strconv.FormatInt(p.cps[0].LegID, 10))
	_ = mw.WriteField("kind", "photo")
	if i >= 0 {
		_ = mw.WriteField("milestone_id", strconv.FormatInt(p.cps[i].ID, 10))
	}
	if withPhone {
		_ = mw.WriteField("lat", strconv.FormatFloat(p.cps[1].Lat, 'f', 4, 64))
		_ = mw.WriteField("lng", strconv.FormatFloat(p.cps[1].Lng, 'f', 4, 64))
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
	want := "Start the drive from " + s.p.cps[strings.Index("ABCD", which)].Name
	if !strings.Contains(s.p.body, want) {
		return fmt.Errorf("the form does not say %q", want)
	}
	return nil
}

// noStopToChoose: the only stop the form ever lets the driver pick is the
// start, and only before the drive has started.
func (s *scWorld) noStopToChoose() error {
	for _, m := range radioRe.FindAllString(s.p.body, -1) {
		if strings.Contains(m, `value=""`) || strings.Contains(m, `value="`+strconv.FormatInt(s.p.cps[0].ID, 10)+`"`) {
			continue
		}
		return fmt.Errorf("the form offers a stop to choose: %s", m)
	}
	return nil
}

func (s *scWorld) nextIs(which string) error {
	want := "Next stop: " + s.p.cps[strings.Index("ABCD", which)].Name
	if !strings.Contains(s.p.body, want) {
		return fmt.Errorf("the form does not say %q", want)
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

func (s *scWorld) arrivedAt(i int, hm string) error {
	c := s.p.cp(i)
	at, ok := parseUTC(c.ArrivedAt)
	if !ok {
		return fmt.Errorf("%s has no arrival: %q (answer %d %s)", c.Name, c.ArrivedAt, s.p.last.Code, s.p.last.Body.String())
	}
	if got := at.In(loadLoc(c.TZ)).Format("2006-01-02 15:04"); got != "2026-11-07 "+hm {
		return fmt.Errorf("%s arrived %s in %s", c.Name, got, c.TZ)
	}
	return nil
}

func (s *scWorld) skipped(i int, why string) error {
	c := s.p.cp(i)
	if c.SkippedAt == "" || c.SkipReason != why || c.ArrivedAt != "" {
		return fmt.Errorf("%s: skipped_at %q reason %q arrived_at %q, want skipped as %q and not arrived", c.Name, c.SkippedAt, c.SkipReason, c.ArrivedAt, why)
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
	name := s.p.cps[strings.Index("ABCD", which)].Name
	if err := s.refusedBody(name); err != nil {
		return err
	}
	if !strings.Contains(s.p.last.Body.String(), " km ") {
		return fmt.Errorf("the refusal does not give a distance: %s", s.p.last.Body.String())
	}
	return nil
}

// noticeHas reads the notice the redirect carries to the post form.
func (s *scWorld) noticeHas(parts ...string) error {
	l := s.p.last
	loc, err := url.Parse(l.Header().Get("Location"))
	if l.Code != http.StatusSeeOther || err != nil {
		return fmt.Errorf("answer %d, location %q", l.Code, l.Header().Get("Location"))
	}
	n := loc.Query().Get("notice")
	for _, w := range parts {
		if !strings.Contains(n, w) {
			return fmt.Errorf("the notice %q does not say %q", n, w)
		}
	}
	return nil
}

// simAtB runs kris's simulated car and moves stop B to where it is now, so the
// car is at B: the trip is on the plains, the corridor is in Ontario.
func (s *scWorld) simAtB() error {
	if err := s.p.sim.started("kris", simCorKGN, 7, 60); err != nil {
		return err
	}
	la, ln, _, _, _, ok := s.p.sim.app.sim.PositionAt("kris", s.p.sim.now)
	if !ok {
		return fmt.Errorf("the simulated car is not running")
	}
	return s.moveStop(1, la, ln)
}

// bus waits for a record of the given event (for stop i, 0-based; -1 = any)
// on the fake Redis, checks a field of it (key "" = none) and that it carries
// no coordinate.
func (s *scWorld) bus(event string, stop int, key, val string) error {
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
				continue
			}
			if key != "" && rec[key] != val {
				return fmt.Errorf("%s says %s %v, want %s", event, key, rec[key], val)
			}
			for k, v := range rec {
				if sv, ok := v.(string); ok && (strings.Contains(sv, s.p.cps[0].Name) || strings.Contains(sv, s.p.cps[1].Name)) && k != "event" {
					return fmt.Errorf("%s carries a stop name in %s", event, k)
				}
			}
			s.p.sim.hudRec = rec
			return s.p.sim.meltHasNoCoordinates()
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("no %s record reached the bus", event)
}
