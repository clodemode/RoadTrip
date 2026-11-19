package main

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/social_preview_photo_card.feature. The shared ones are
// bound in social_card_steps_test.go; this reads its world through getters and
// hands a page back through setPage.

func initSocialPhotoCardSteps(t *testing.T, sc *godog.ScenarioContext, getApp func() *App, getKris func() *http.Cookie,
	getRec func() *httptest.ResponseRecorder, setPage func(*httptest.ResponseRecorder), getVisitor func() string) {
	var (
		posts   []*Post
		kept    string
		fetched *httptest.ResponseRecorder
		other   int64
		addrs   []string
	)
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		posts, kept, fetched, other, addrs = nil, "", nil, 0, nil
		return ctx, nil
	})
	local := func(u string) string { return strings.TrimPrefix(u, "https://example.com") }
	tags := func() []socialTag { return socialTags(getRec().Body.String()) }
	post := func(seed int64, body string) error {
		posts = append(posts, putTestPhoto(t, getApp(), 1600, 900, seed, "", body))
		return nil
	}
	read := func(c *http.Cookie, target string) error {
		r := doAs(t, getApp(), c, "GET", target, nil)
		if r.Code != http.StatusOK && r.Code != http.StatusNotFound {
			return fmt.Errorf("%s answered %d", target, r.Code)
		}
		setPage(r)
		return nil
	}
	photoURL := func() string { return fmt.Sprintf("/photo/%d", posts[0].ID) }
	cardOf := func(page string) string { return socialValue(socialTags(page), "og:image") }

	sc.Step(`^the trip-keeper has posted a photo on the first day$`, func() error { return post(1, "") })
	sc.Step(`^the trip-keeper has posted a second, different photo on the first day$`, func() error { return post(2, "") })
	sc.Step(`^the trip-keeper has posted a photo on the first day with a long note$`, func() error {
		return post(1, strings.Repeat("We stopped for pie and the light was good on the water. ", 8))
	})
	sc.Step(`^a visitor reads the photo page$`, func() error { return read(nil, photoURL()) })
	sc.Step(`^the driver reads the photo page$`, func() error { return read(getKris(), photoURL()) })
	sc.Step(`^the page names the photo card as an absolute https jpeg address under "([^"]*)"$`, func(under string) error {
		img := getVisitor()
		if !strings.HasPrefix(img, "https://example.com"+under) {
			return fmt.Errorf("og:image %q is not under %s", img, under)
		}
		p, q, _ := strings.Cut(img, "?")
		if !strings.HasSuffix(p, ".jpg") || !strings.HasPrefix(q, "v=") || len(q) < 4 {
			return fmt.Errorf("og:image %q does not end .jpg before a ?v= query", img)
		}
		return nil
	})
	sc.Step(`^the photo card is declared 1200 by 630 as image/jpeg with a description$`, func() error {
		for k, want := range map[string]string{"og:image:width": "1200", "og:image:height": "630", "og:image:type": "image/jpeg"} {
			if got := socialValue(tags(), k); got != want {
				return fmt.Errorf("%s is %q, want %q", k, got, want)
			}
		}
		if socialValue(tags(), "og:image:alt") == "" {
			return fmt.Errorf("og:image:alt is empty")
		}
		return nil
	})
	sc.Step(`^the Twitter image is the same address$`, func() error {
		if a, b := socialValue(tags(), "og:image"), socialValue(tags(), "twitter:image"); a == "" || a != b {
			return fmt.Errorf("og:image %q, twitter:image %q", a, b)
		}
		return nil
	})
	sc.Step(`^a visitor fetches the photo card address the page names$`, func() error {
		fetched = doAs(t, getApp(), nil, "GET", local(getVisitor()), nil)
		return nil
	})
	sc.Step(`^the photo card answer is 200 image/jpeg and may be cached for a day$`, func() error {
		if fetched.Code != http.StatusOK {
			return fmt.Errorf("answered %d", fetched.Code)
		}
		if ct := fetched.Header().Get("Content-Type"); ct != "image/jpeg" {
			return fmt.Errorf("Content-Type is %q", ct)
		}
		if cc := fetched.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
			return fmt.Errorf("Cache-Control is %q", cc)
		}
		return nil
	})
	sc.Step(`^the photo card bytes decode to exactly 1200 by 630 as a jpeg and are under 300 KB$`, func() error {
		b := fetched.Body.Bytes()
		if len(b) >= 300*1024 {
			return fmt.Errorf("%d bytes, want under 307200", len(b))
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("not a JPEG: %v", err)
		}
		if cfg.Width != 1200 || cfg.Height != 630 {
			return fmt.Errorf("%dx%d, want 1200x630", cfg.Width, cfg.Height)
		}
		return nil
	})
	sc.Step(`^the photo card address is remembered$`, func() error { kept = getVisitor(); return nil })
	sc.Step(`^the photo card address is the remembered one$`, func() error {
		if kept == "" || getVisitor() != kept {
			return fmt.Errorf("the address is %q, it was %q", getVisitor(), kept)
		}
		return nil
	})
	sc.Step(`^the photo is removed$`, func() error { return getApp().removePhoto(posts[0].ID, time.Now()) })
	sc.Step(`^the removal is undone$`, func() error { return getApp().undoPhoto(posts[0].ID, time.Now()) })
	sc.Step(`^the photo page answers 404$`, func() error {
		if r := doAs(t, getApp(), nil, "GET", photoURL(), nil); r.Code != http.StatusNotFound {
			return fmt.Errorf("answered %d", r.Code)
		}
		return nil
	})
	sc.Step(`^a visitor fetches the remembered photo card address$`, func() error {
		fetched = doAs(t, getApp(), nil, "GET", local(kept), nil)
		return nil
	})
	sc.Step(`^the photo card address answers 404$`, func() error {
		if fetched.Code != http.StatusNotFound {
			return fmt.Errorf("answered %d", fetched.Code)
		}
		return nil
	})
	sc.Step(`^no page of the ride names a photo card$`, func() error {
		for _, p := range []string{"/", "/photos", "/legs/day-1-ridge"} {
			r := doAs(t, getApp(), nil, "GET", p, nil)
			if strings.Contains(r.Body.String(), "/og/photo/") {
				return fmt.Errorf("%s still names a photo card", p)
			}
		}
		return nil
	})
	sc.Step(`^the same photo is on a post of another ride$`, func() error {
		db := getApp().store.db
		var phid int64
		if err := db.QueryRow(`SELECT photo_id FROM posts WHERE id=?`, posts[0].ID).Scan(&phid); err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO rides(slug,name) VALUES('elsewhere','Elsewhere')`); err != nil {
			return err
		}
		res, err := db.Exec(`INSERT INTO legs(ride_slug,ord,slug,name,date) VALUES('elsewhere',1,'else-1','Else','2030-06-01')`)
		if err != nil {
			return err
		}
		leg, _ := res.LastInsertId()
		res, err = db.Exec(`INSERT INTO posts(leg_id,kind,occurred_at,tz,photo_id,created_at) VALUES(?,?,?,?,?,?)`,
			leg, "photo", "2030-06-01T12:00:00Z", "UTC", phid, "2030-06-01T12:00:00Z")
		if err != nil {
			return err
		}
		other, _ = res.LastInsertId()
		return nil
	})
	sc.Step(`^a visitor fetches the photo card of that post$`, func() error {
		fetched = doAs(t, getApp(), nil, "GET", fmt.Sprintf("/og/photo/%d.jpg", other), nil)
		return nil
	})
	sc.Step(`^a visitor fetches the photo card of the photo$`, func() error {
		fetched = doAs(t, getApp(), nil, "GET", fmt.Sprintf("/og/photo/%d.jpg", posts[0].ID), nil)
		return nil
	})
	sc.Step(`^the photo card request is sent to sign in$`, func() error {
		if fetched.Code != http.StatusFound || !strings.HasPrefix(fetched.Header().Get("Location"), "/driver/login") {
			return fmt.Errorf("answered %d to %q", fetched.Code, fetched.Header().Get("Location"))
		}
		return nil
	})
	sc.Step(`^the strip handed to the drawing code reads "([^"]*)" over "([^"]*)"$`, func(name, line string) error {
		p, rv, ok := getApp().visitorPhoto(posts[0].ID)
		if !ok {
			return fmt.Errorf("the photo is not visible")
		}
		if s := photoStripOf(p, rv); s.Name != name || s.Line != line {
			return fmt.Errorf("the strip is %+v", s)
		}
		return nil
	})
	sc.Step(`^the strip adds no checkpoint name, no place and no clock time to the trip's name$`, func() error {
		p, rv, _ := getApp().visitorPhoto(posts[0].ID)
		s := photoStripOf(p, rv)
		// the trip's own name may hold a place (Alder to Elm); everything the strip adds to it may not
		text := s.Line + "\n" + strings.ReplaceAll(s.alt(), s.Name, "")
		names := 0
		for _, l := range rv.Legs {
			for _, c := range l.Checkpoints {
				names++
				if c.Name != "" && strings.Contains(strings.ToLower(text), strings.ToLower(c.Name)) {
					return fmt.Errorf("the strip names the checkpoint %q", c.Name)
				}
			}
		}
		if names == 0 {
			return fmt.Errorf("the ride has no checkpoint to look for")
		}
		if regexp.MustCompile(`\d{1,2}:\d{2}`).MatchString(text) {
			return fmt.Errorf("the strip holds a clock time: %q", text)
		}
		return nil
	})
	sc.Step(`^the card addresses of both photo pages are read$`, func() error {
		addrs = nil
		for _, p := range posts {
			r := doAs(t, getApp(), nil, "GET", fmt.Sprintf("/photo/%d", p.ID), nil)
			addrs = append(addrs, cardOf(r.Body.String()))
		}
		return nil
	})
	sc.Step(`^the two card addresses differ$`, func() error {
		if len(addrs) != 2 || addrs[0] == "" || addrs[1] == "" || addrs[0] == addrs[1] {
			return fmt.Errorf("addresses %q", addrs)
		}
		return nil
	})
	sc.Step(`^reading the first photo page again names the same address$`, func() error {
		r := doAs(t, getApp(), nil, "GET", photoURL(), nil)
		if got := cardOf(r.Body.String()); got != addrs[0] {
			return fmt.Errorf("%q, was %q", got, addrs[0])
		}
		return nil
	})
	sc.Step(`^the photo's file is not a picture$`, func() error {
		for _, rel := range photoPaths(getApp(), posts[0]) {
			if err := os.WriteFile(getApp().mediaFile(rel), []byte("not a picture"), 0o644); err != nil {
				return err
			}
		}
		return nil
	})
	own := func(title, desc string) error {
		rv, err := getApp().loadRide()
		if err != nil {
			return err
		}
		def := rv.Ride.Name + ": a one-way drive, live from the road."
		if title == "" || desc == "" || desc == def {
			return fmt.Errorf("title %q, description %q", title, desc)
		}
		return nil
	}
	sc.Step(`^the page "([^"]*)" has its own preview title and description$`, func(p string) error {
		r := doAs(t, getApp(), nil, "GET", p, nil)
		ts := socialTags(r.Body.String())
		return own(socialValue(ts, "og:title"), socialValue(ts, "og:description"))
	})
	sc.Step(`^the photo page has its own preview title and description$`, func() error {
		r := doAs(t, getApp(), nil, "GET", photoURL(), nil)
		ts := socialTags(r.Body.String())
		return own(socialValue(ts, "og:title"), socialValue(ts, "og:description"))
	})
	sc.Step(`^the preview description is the note cut at a word with an ellipsis and under 160 characters$`, func() error {
		d := socialValue(tags(), "og:description")
		if n := len([]rune(d)); n > 160 || !strings.HasSuffix(d, "…") || !strings.HasPrefix(d, "We stopped for pie") ||
			strings.HasSuffix(strings.TrimSuffix(d, "…"), " ") {
			return fmt.Errorf("description %q (%d characters)", d, n)
		}
		return nil
	})
}
