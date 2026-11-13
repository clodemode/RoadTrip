package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"

	"github.com/cucumber/godog"
)

// Steps for features/photo_opens_fitted_to_the_screen.feature.

type photoViewWorld struct {
	p      *postWorld
	last   *httptest.ResponseRecorder
	postID int64
}

var (
	viewHrefRe  = regexp.MustCompile(`href="/photo/(\d+)"`)
	rawMediaRe  = regexp.MustCompile(`href="/media/[^"]*"`)
	coordLikeRe = regexp.MustCompile(`-?\d{1,3}\.\d{3,}`)
)

func initPhotoViewScenarios(sc *godog.ScenarioContext, p *postWorld) {
	w := &photoViewWorld{p: p}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = photoViewWorld{p: p}
		return ctx, nil
	})
	sc.Step(`^kris has posted a photo$`, func() error {
		if err := p.post(""); err != nil {
			return err
		}
		posts, err := p.sim.app.store.Posts("post-photo-trip", 0)
		if err != nil || len(posts) != 1 || posts[0].Photo == nil {
			return fmt.Errorf("want one photo post, have %d (%v)", len(posts), err)
		}
		w.postID = posts[0].ID
		return nil
	})
	sc.Step(`^a visitor opens the first leg's page$`, func() error {
		rv, err := p.sim.app.loadRide()
		if err != nil {
			return err
		}
		return w.get(nil, "/legs/"+rv.Legs[0].Slug)
	})
	sc.Step(`^a visitor opens the photos page$`, func() error { return w.get(nil, "/photos") })
	sc.Step(`^a visitor opens the photo's viewer page$`, func() error { return w.get(nil, w.viewURL(w.postID)) })
	sc.Step(`^kris opens the photo's viewer page$`, func() error { return w.get(p.sim.cookies["kris"], w.viewURL(w.postID)) })
	sc.Step(`^a visitor opens the viewer page of post (\d+)$`, func(n int) error {
		return w.get(nil, fmt.Sprintf("/photo/%d", n))
	})
	sc.Step(`^the photo links to its viewer page$`, func() error {
		m := viewHrefRe.FindStringSubmatch(w.last.Body.String())
		if m == nil || m[1] != fmt.Sprint(w.postID) {
			return fmt.Errorf("no link to /photo/%d on the page", w.postID)
		}
		return nil
	})
	sc.Step(`^no link on the page goes straight to a full-size photo file$`, func() error {
		if m := rawMediaRe.FindString(w.last.Body.String()); m != "" {
			return fmt.Errorf("the page still carries %s", m)
		}
		return nil
	})
	sc.Step(`^the viewer holds the photo with the fit-to-screen class$`, func() error {
		if w.last.Code != 200 {
			return fmt.Errorf("viewer answered %d", w.last.Code)
		}
		b := w.last.Body.String()
		if !regexp.MustCompile(`<img[^>]*class="photo-view-fit"[^>]*src="/media/[^"]+\.jpg"`).MatchString(b) &&
			!regexp.MustCompile(`<img[^>]*src="/media/[^"]+\.jpg"[^>]*class="photo-view-fit"`).MatchString(b) {
			return fmt.Errorf("no fitted photo image in the viewer")
		}
		return nil
	})
	sc.Step(`^the viewer has a link back to the post on its leg page$`, func() error {
		rv, _ := p.sim.app.loadRide()
		want := fmt.Sprintf(`href="/legs/%s#post-%d"`, rv.Legs[0].Slug, w.postID)
		if !strings.Contains(w.last.Body.String(), want) {
			return fmt.Errorf("no %s in the viewer", want)
		}
		return nil
	})
	sc.Step(`^the viewer has a link called "([^"]*)"$`, func(label string) error {
		if !regexp.MustCompile(`<a[^>]*href="/media/[^"]+"[^>]*>` + regexp.QuoteMeta(label) + `</a>`).MatchString(w.last.Body.String()) {
			return fmt.Errorf("no %q link to the file", label)
		}
		return nil
	})
	sc.Step(`^the viewer carries no coordinates$`, func() error {
		body := regexp.MustCompile(`(?s)<section class="photo-view">.*?</section>`).FindString(w.last.Body.String())
		if body == "" {
			return fmt.Errorf("no viewer section")
		}
		if m := coordLikeRe.FindString(body); m != "" {
			return fmt.Errorf("the viewer carries %q", m)
		}
		return nil
	})
	sc.Step(`^the same photo is on a post of a ride the site is not showing$`, func() error {
		db := p.sim.app.store.db
		var phid int64
		if err := db.QueryRow(`SELECT photo_id FROM posts WHERE id=?`, w.postID).Scan(&phid); err != nil {
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
		w.postID, _ = res.LastInsertId()
		return nil
	})
	sc.Step(`^a visitor opens the viewer page of that post$`, func() error { return w.get(nil, w.viewURL(w.postID)) })
	sc.Step(`^the answer is "not found"$`, func() error {
		if w.last.Code != http.StatusNotFound {
			return fmt.Errorf("answered %d, want 404", w.last.Code)
		}
		return nil
	})
	sc.Step(`^the map's photo popup links to the viewer, not the original$`, func() error {
		b, err := staticFS.ReadFile("static/js/map.js")
		if err != nil {
			return err
		}
		s := string(b)
		if !strings.Contains(s, `'<a href="/photo/' + p.id + '"><img class="popup-photo"`) || strings.Contains(s, `esc(p.full) + '"><img`) {
			return fmt.Errorf("the popup photo does not link to /photo/<id>")
		}
		return nil
	})
}

func (w *photoViewWorld) viewURL(id int64) string { return fmt.Sprintf("/photo/%d", id) }

func (w *photoViewWorld) get(c *http.Cookie, target string) error {
	w.last = doAs(w.p.t, w.p.sim.app, c, "GET", target, nil)
	if w.last.Code != 200 && w.last.Code != 404 {
		return fmt.Errorf("GET %s answered %d", target, w.last.Code)
	}
	return nil
}
