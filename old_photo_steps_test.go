package main

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/old_photo_without_place.feature. They ride the start-and-
// check-in world (stops A to D along a line) and add photos dated against the
// clock of the run, earlier posts that make up the car's trail, and the form
// fields a driver's pin choice sends. Nothing calls a real service.
type opWorld struct {
	p       *postWorld
	s       *scWorld
	takenAt time.Time   // the photo's own clock, when it has one
	exactAt *[2]float64 // the exact point of the earlier post at a private place
	fields  map[string]string
}

func initOldPhotoScenarios(sc *godog.ScenarioContext, s *scWorld) {
	w := &opWorld{p: s.p, s: s}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = opWorld{p: s.p, s: s}
		return ctx, nil
	})
	sc.Step(`^a photo taken (\d+) (hours?|minutes?) ago with no GPS of its own$`, func(n int, unit string) error {
		w.takenAgo(n, unit)
		w.p.photo = jpegTaken(w.wall())
		return nil
	})
	sc.Step(`^a photo taken (\d+) (hours?|minutes?) ago with its GPS at ([ABCD])$`, func(n int, unit, which string) error {
		w.takenAgo(n, unit)
		i := strings.Index("ABCD", which)
		w.p.photo = jpegGPSAt(w.wall(), w.p.cps[i].Lat, w.p.cps[i].Lng)
		return nil
	})
	sc.Step(`^the phone reports a position at D$`, func() error {
		s.phone, s.phoneAt = true, &[2]float64{w.p.cps[3].Lat, w.p.cps[3].Lng}
		return nil
	})
	sc.Step(`^an earlier post pinned between ([ABC]) and ([BCD]), made (\d+) minutes (before|after) the photo was taken$`,
		func(a, b string, m int, dir string) error {
			i, j := strings.Index("ABCD", a), strings.Index("ABCD", b)
			return w.earlier((stopLat(i)+stopLat(j))/2, stopsLng, m, dir)
		})
	sc.Step(`^an earlier post pinned at ([ABCD]), made (\d+) minutes (before|after) the photo was taken$`,
		func(a string, m int, dir string) error {
			i := strings.Index("ABCD", a)
			return w.earlier(w.p.cps[i].Lat, w.p.cps[i].Lng, m, dir)
		})
	sc.Step(`^stop C is a private place$`, func() error {
		db := w.p.sim.app.store.db
		if _, err := db.Exec(`UPDATE checkpoints SET lat=?, lng=?, precision=? WHERE id=?`,
			41.0123, -100.0131, precisionFuzzed, w.p.cps[2].ID); err != nil {
			return err
		}
		return s.reload(len(w.p.cps))
	})
	sc.Step(`^an earlier post pinned exactly near C, made (\d+) minutes (before|after) the photo was taken$`,
		func(m int, dir string) error {
			w.exactAt = &[2]float64{41.0128, -100.0134}
			return w.earlier(w.exactAt[0], w.exactAt[1], m, dir)
		})

	sc.Step(`^kris posts that photo choosing the phone's location$`, func() error {
		w.fields = map[string]string{"pin_source": "browser"}
		return w.send()
	})
	sc.Step(`^kris posts that photo choosing no pin$`, func() error {
		w.fields = map[string]string{"pin_source": "none"}
		return w.send()
	})
	sc.Step(`^kris posts that photo with the pin source the page pre-selected$`, func() error {
		w.fields = map[string]string{"pin_source": "browser", "pin_auto": "1"}
		return w.send()
	})

	sc.Step(`^the photo post exists$`, func() error { _, err := w.photoPost(); return err })
	sc.Step(`^the photo post has no pin$`, func() error {
		post, err := w.photoPost()
		if err != nil {
			return err
		}
		if post.Lat != nil || post.Lng != nil || post.PinSource != pinNone {
			return fmt.Errorf("the post has a pin: source %q", post.PinSource)
		}
		return nil
	})
	sc.Step(`^the photo post's pin source is "([a-z]+)"$`, func(want string) error {
		post, err := w.photoPost()
		if err != nil {
			return err
		}
		if post.PinSource != want {
			return fmt.Errorf("the post's pin source is %q, want %q", post.PinSource, want)
		}
		return nil
	})
	sc.Step(`^the photo post is pinned between ([ABC]) and ([BCD])$`, func(a, b string) error {
		i, j := strings.Index("ABCD", a), strings.Index("ABCD", b)
		return w.pinnedAt((stopLat(i)+stopLat(j))/2, stopsLng, 5, "between "+a+" and "+b)
	})
	sc.Step(`^the photo post is pinned at the phone's position$`, func() error {
		lat, lng := w.p.cps[1].Lat, w.p.cps[1].Lng
		if w.s.phoneAt != nil {
			lat, lng = w.s.phoneAt[0], w.s.phoneAt[1]
		}
		return w.pinnedAt(lat, lng, 1, "the phone's position")
	})
	sc.Step(`^the photo post is pinned on C's published point$`, func() error {
		c := w.p.cp(2)
		return w.pinnedAt(c.Lat, c.Lng, 0.5, "C's published point")
	})
	sc.Step(`^the photo post is not pinned at the earlier post's exact point$`, func() error {
		post, err := w.photoPost()
		if err != nil {
			return err
		}
		if post.Lat == nil || w.exactAt == nil {
			return fmt.Errorf("no pin, or no exact point to compare with")
		}
		if haversineKm(*post.Lat, *post.Lng, w.exactAt[0], w.exactAt[1]) < 0.3 {
			return fmt.Errorf("the post is pinned at the earlier post's exact point")
		}
		if post.PinSource == "trail" && !post.PinFuzzed {
			return fmt.Errorf("a trail pin at a private place is not marked fuzzed")
		}
		return nil
	})
	sc.Step(`^the photo post is dated (\d+) hours? ago$`, func(n int) error {
		return w.dated(time.Now().Add(-time.Duration(n) * time.Hour))
	})
	sc.Step(`^the photo post is dated now$`, func() error { return w.dated(time.Now()) })
	sc.Step(`^no stop was checked in$`, func() error {
		for _, i := range []int{1, 2, 3} {
			c := w.p.cp(i)
			if c.ArrivedAt != "" || c.SkippedAt != "" {
				return fmt.Errorf("%s changed: arrived %q skipped %q", c.Name, c.ArrivedAt, c.SkippedAt)
			}
		}
		return nil
	})
	sc.Step(`^the notice says the photo was taken (\d+) hours? ago and pinned where the car was then$`, func(n int) error {
		return s.noticeHas("taken "+strconv.Itoa(n)+" hour", "pinned where the car was then")
	})
	sc.Step(`^the notice says the photo was taken (\d+) hours? ago and left off the map$`, func(n int) error {
		return s.noticeHas("taken "+strconv.Itoa(n)+" hour", "left off the map")
	})
	sc.Step(`^the notice says nothing was checked in$`, func() error { return s.noticeHas("nothing was checked in") })
	sc.Step(`^the pin line says the photo was taken (\d+) hours? ago and pinned where the car was then$`, func(n int) error {
		return w.pinLineHas("taken "+strconv.Itoa(n)+" hour", "pinned where the car was then")
	})
	sc.Step(`^the pin line says the photo was taken (\d+) hours? ago and left off the map$`, func(n int) error {
		return w.pinLineHas("taken "+strconv.Itoa(n)+" hour", "left off the map")
	})
	sc.Step(`^the pin line shows no exact point$`, func() error {
		line, err := w.pinLine("taken 3 hour")
		if err != nil {
			return err
		}
		for _, bad := range []string{"41.012", "-100.013", "exact "} {
			if strings.Contains(line, bad) {
				return fmt.Errorf("the pin line shows %q: %s", bad, line)
			}
		}
		return nil
	})
	sc.Step(`^the form script reads the photo's time and says an old photo is pinned from the car's trail$`, func() error {
		js, err := w.script()
		if err != nil {
			return err
		}
		if !strings.Contains(js, "DateTimeOriginal") || !strings.Contains(js, "car’s trail") {
			return fmt.Errorf("post.js does not read the photo's time and name the car's trail")
		}
		return nil
	})
	sc.Step(`^the form script marks a pin source it pre-selected itself$`, func() error {
		js, err := w.script()
		if err != nil {
			return err
		}
		if !strings.Contains(js, "pin_auto") || !strings.Contains(w.p.body, `name="pin_auto"`) {
			return fmt.Errorf("the form has no pin_auto field")
		}
		return nil
	})
}

func (w *opWorld) legTZ() string {
	rv, err := w.p.sim.app.loadRide()
	if err != nil || len(rv.Legs) == 0 {
		w.p.t.Fatalf("no leg: %v", err)
	}
	return rv.Legs[0].TZ
}

func (w *opWorld) takenAgo(n int, unit string) {
	d := time.Duration(n) * time.Hour
	if strings.HasPrefix(unit, "minute") {
		d = time.Duration(n) * time.Minute
	}
	w.takenAt = time.Now().Add(-d).Truncate(time.Second)
}

// wall is the photo's clock as the camera writes it: wall time in the form's zone.
func (w *opWorld) wall() string {
	return w.takenAt.In(loadLoc(w.legTZ())).Format("2006:01:02 15:04:05")
}

// earlier files a post the car's trail is made of: an exact pin, as a phone or
// a photo's own GPS gave it.
func (w *opWorld) earlier(lat, lng float64, m int, dir string) error {
	at := w.takenAt.Add(-time.Duration(m) * time.Minute)
	if dir == "after" {
		at = w.takenAt.Add(time.Duration(m) * time.Minute)
	}
	return w.p.sim.app.store.CreatePost(&Post{LegID: w.p.cps[0].LegID, Kind: "photo", TZ: w.legTZ(),
		OccurredAt: utcString(at), Lat: &lat, Lng: &lng, PinSource: pinBrowser,
		CreatedAt: utcString(at.Add(time.Minute))})
}

// send posts the form with the photo, the phone's position when it has one,
// and the fields the scenario adds.
func (w *opWorld) send() error {
	p := w.p
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("leg_id", strconv.FormatInt(p.cps[0].LegID, 10))
	_ = mw.WriteField("kind", "photo")
	if w.s.phone {
		lat, lng := p.cps[1].Lat, p.cps[1].Lng
		if w.s.phoneAt != nil {
			lat, lng = w.s.phoneAt[0], w.s.phoneAt[1]
		}
		_ = mw.WriteField("lat", strconv.FormatFloat(lat, 'f', 4, 64))
		_ = mw.WriteField("lng", strconv.FormatFloat(lng, 'f', 4, 64))
	}
	for k, v := range w.fields {
		_ = mw.WriteField(k, v)
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
	return nil
}

// photoPost is the one post that carries a photo; earlier posts have none.
func (w *opWorld) photoPost() (*Post, error) {
	posts, err := w.p.sim.app.store.Posts("post-photo-trip", 0)
	if err != nil {
		return nil, err
	}
	var found []Post
	for _, p := range posts {
		if p.PhotoID != 0 {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("want one photo post, have %d (answer %d %s)", len(found), w.p.last.Code, w.p.last.Body.String())
	}
	return &found[0], nil
}

func (w *opWorld) pinnedAt(lat, lng, withinKm float64, what string) error {
	post, err := w.photoPost()
	if err != nil {
		return err
	}
	if post.Lat == nil || post.Lng == nil {
		return fmt.Errorf("the post has no pin (source %q), want it at %s", post.PinSource, what)
	}
	if km := haversineKm(*post.Lat, *post.Lng, lat, lng); km > withinKm {
		return fmt.Errorf("the post is pinned %.1f km from %s (source %q)", km, what, post.PinSource)
	}
	return nil
}

func (w *opWorld) dated(want time.Time) error {
	post, err := w.photoPost()
	if err != nil {
		return err
	}
	got, ok := parseUTC(post.OccurredAt)
	if !ok {
		return fmt.Errorf("the post's time %q does not parse", post.OccurredAt)
	}
	if d := got.Sub(want); d > 2*time.Minute || d < -2*time.Minute {
		return fmt.Errorf("the post is dated %s, want about %s", post.OccurredAt, utcString(want))
	}
	return nil
}

var pinLineRe = regexp.MustCompile(`<p class="post-pin[^"]*">([^<]*)</p>`)

// pinLine is the signed-in pin line that names the age; the home page carries one per post.
func (w *opWorld) pinLine(contains string) (string, error) {
	r := doAs(w.p.t, w.p.sim.app, w.p.sim.cookies["kris"], "GET", "/", nil)
	var all []string
	for _, m := range pinLineRe.FindAllStringSubmatch(r.Body.String(), -1) {
		all = append(all, m[1])
		if strings.Contains(m[1], contains) {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("no pin line says %q (answer %d; lines %q)", contains, r.Code, all)
}

func (w *opWorld) pinLineHas(parts ...string) error {
	line, err := w.pinLine(parts[0])
	if err != nil {
		return err
	}
	for _, p := range parts {
		if !strings.Contains(line, p) {
			return fmt.Errorf("the pin line %q does not say %q", line, p)
		}
	}
	return nil
}

func (w *opWorld) script() (string, error) {
	r := doAs(w.p.t, w.p.sim.app, w.p.sim.cookies["kris"], "GET", "/static/js/post.js", nil)
	if r.Code != http.StatusOK {
		return "", fmt.Errorf("post.js answered %d", r.Code)
	}
	return r.Body.String(), nil
}
