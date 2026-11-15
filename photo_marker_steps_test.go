package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/photo_post_becomes_a_marker.feature. The photos are rows
// and files written straight into a throwaway volume, so a scenario can look at
// exactly which bytes are where.

type pmPost struct {
	id       int64
	title    string
	photoID  int64
	thumbRel string
	origRel  string
	occurred string
	tz       string
	lat, lng float64
}

type pmWorld struct {
	t     *testing.T
	app   *App
	kris  *http.Cookie
	now   time.Time
	posts map[string]*pmPost
	last  *http.Request
	code  int
	geo   map[int64][2]float64 // published point per post before the removal
	form  string
}

func initPhotoMarkerScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &pmWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = pmWorld{t: t, posts: map[string]*pmPost{}, now: time.Now()}
		return ctx, nil
	})
	sc.Step(`^the trip is active and kris is signed in$`, func() error {
		w.app = activeTripApp(t)
		w.app.clockFn = func() time.Time { return w.now }
		w.kris = asUser(t, w.app, "kris", roleAdmin)
		return nil
	})
	sc.Step(`^the ride has two photo posts "([^"]*)" and "([^"]*)", each pinned and held on the public media path$`, func(a, b string) error {
		legs := mustLegs(t, w.app, w.app.activeSlug())
		for i, title := range []string{a, b} {
			p := &pmPost{title: title, tz: "America/Chicago", lat: 31.5 + float64(i)/10, lng: -97.2,
				occurred: utcString(time.Now().Add(-time.Duration(3-i) * time.Hour))}
			sha := fmt.Sprintf("%016x%048x", i+1, 0)
			p.origRel = "2026/11/" + sha[:16] + ".jpg"
			p.thumbRel = "2026/11/" + sha[:16] + "_t.jpg"
			for _, rel := range []string{p.origRel, p.thumbRel} {
				full := filepath.Join(w.app.mediaDir, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(full, []byte("not a real picture "+title), 0o644); err != nil {
					return err
				}
			}
			lat, lng := p.lat, p.lng
			ph := &Photo{Path: p.origRel, ThumbPath: p.thumbRel, TZ: p.tz, Lat: &lat, Lng: &lng, Width: 800, Height: 600, SHA256: sha}
			if err := w.app.store.CreatePhoto(ph); err != nil {
				return err
			}
			p.photoID = ph.ID
			post := &Post{LegID: legs[0].ID, Kind: "photo", OccurredAt: p.occurred, TZ: p.tz, Title: title, PhotoID: ph.ID,
				Lat: &lat, Lng: &lng, PinSource: pinBrowser}
			if err := w.app.store.CreatePost(post); err != nil {
				return err
			}
			p.id = post.ID
			w.posts[title] = p
		}
		w.geo = w.published()
		return nil
	})

	sc.Step(`^kris taps Remove photo on the post "([^"]*)"$`, func(title string) error { return w.act(w.kris, "/driver/post/"+w.id(title)+"/photo/remove", nil) })
	sc.Step(`^kris taps Undo on the post "([^"]*)" (\d+) minutes later$`, func(title string, m int) error {
		w.now = w.now.Add(time.Duration(m) * time.Minute)
		if err := w.act(w.kris, "/driver/post/"+w.id(title)+"/photo/undo", nil); err != nil {
			return err
		}
		return nil
	})
	sc.Step(`^a visitor who is not signed in posts Remove photo for the post "([^"]*)"$`, func(title string) error {
		return w.act(nil, "/driver/post/"+w.id(title)+"/photo/remove", nil)
	})
	sc.Step(`^(\d+) minutes pass and the holding folder is swept$`, func(m int) error {
		w.now = w.now.Add(time.Duration(m) * time.Minute)
		w.app.sweepPhotoHolds(w.now)
		return nil
	})
	sc.Step(`^kris taps Undo on the post "([^"]*)" and is told there is nothing left to undo$`, func(title string) error {
		rec := doAs(t, w.app, w.kris, "POST", "/driver/post/"+w.id(title)+"/photo/undo", url.Values{})
		loc := rec.Header().Get("Location")
		if rec.Code != http.StatusSeeOther || !strings.Contains(loc, "nothing+to+undo") {
			return fmt.Errorf("answered %d to %q", rec.Code, loc)
		}
		return nil
	})
	sc.Step(`^kris opens /driver/posts$`, func() error {
		rec := doAs(t, w.app, w.kris, "GET", "/driver/posts", nil)
		if rec.Code != http.StatusOK {
			return fmt.Errorf("/driver/posts answered %d", rec.Code)
		}
		w.form = rec.Body.String()
		return nil
	})
	sc.Step(`^the page lists both photo posts with a checkbox each and no dialog$`, func() error {
		for _, p := range w.posts {
			if !strings.Contains(w.form, fmt.Sprintf(`name="post_id" value="%d"`, p.id)) {
				return fmt.Errorf("no checkbox for %q", p.title)
			}
		}
		for _, bad := range []string{"<dialog", "confirm(", "alert(", "data-bs-toggle=\"modal\""} {
			if strings.Contains(w.form, bad) {
				return fmt.Errorf("the page holds %q", bad)
			}
		}
		return nil
	})
	sc.Step(`^kris ticks both and taps Remove photos$`, func() error {
		f := url.Values{}
		for _, p := range w.posts {
			f.Add("post_id", strconv.FormatInt(p.id, 10))
		}
		rec := doAs(t, w.app, w.kris, "POST", "/driver/posts/photos/remove", f)
		if rec.Code != http.StatusSeeOther {
			return fmt.Errorf("bulk removal answered %d", rec.Code)
		}
		return nil
	})

	sc.Step(`^the post "([^"]*)" keeps the post with the same time, zone and pin$`, func(title string) error {
		p := w.posts[title]
		got, err := w.app.store.Post(p.id)
		if err != nil {
			return fmt.Errorf("the post is gone: %v", err)
		}
		if got.OccurredAt != p.occurred || got.TZ != p.tz {
			return fmt.Errorf("time/zone changed: %s %s", got.OccurredAt, got.TZ)
		}
		if got.Lat == nil || got.Lng == nil || *got.Lat != p.lat || *got.Lng != p.lng || got.PinSource != pinBrowser {
			return fmt.Errorf("the pin changed: %v %v %s", got.Lat, got.Lng, got.PinSource)
		}
		return nil
	})
	sc.Step(`^the post "([^"]*)" has no photo$`, func(title string) error {
		got, err := w.app.store.Post(w.posts[title].id)
		if err != nil {
			return err
		}
		if got.PhotoID != 0 || got.Photo != nil {
			return fmt.Errorf("the post still has photo %d", got.PhotoID)
		}
		return nil
	})
	sc.Step(`^the post "([^"]*)" still has its photo$`, func(title string) error { return w.hasPhoto(title) })
	sc.Step(`^the post "([^"]*)" has its photo again$`, func(title string) error { return w.hasPhoto(title) })
	sc.Step(`^both posts are still on the ride$`, func() error {
		for _, p := range w.posts {
			if _, err := w.app.store.Post(p.id); err != nil {
				return fmt.Errorf("post %q is gone", p.title)
			}
		}
		return nil
	})
	sc.Step(`^the photo's image address answers (\d+)$`, func(want int) error { return w.media(w.posts["Bad one"].origRel, want) })
	sc.Step(`^the photo's thumbnail address answers (\d+)$`, func(want int) error { return w.media(w.posts["Bad one"].thumbRel, want) })
	sc.Step(`^a visitor sees the post "([^"]*)" on the home page with no picture$`, func(title string) error {
		rec := doAs(t, w.app, nil, "GET", "/", nil)
		body := rec.Body.String()
		p := w.posts[title]
		if !strings.Contains(body, fmt.Sprintf(`id="post-%d"`, p.id)) || !strings.Contains(body, title) {
			return fmt.Errorf("the marker for %q is not on the page", title)
		}
		if strings.Contains(body, p.thumbRel) || strings.Contains(body, p.origRel) {
			return fmt.Errorf("the page still names the removed picture")
		}
		return nil
	})
	sc.Step(`^the ride's map data still has a point for the post "([^"]*)" at the same place$`, func(title string) error {
		got := w.published()[w.posts[title].id]
		if want, ok := w.geo[w.posts[title].id]; !ok || got != want {
			return fmt.Errorf("the point moved or vanished: %v, was %v", got, want)
		}
		return nil
	})
	sc.Step(`^no file of that photo is under the public media path$`, func() error {
		p := w.posts["Bad one"]
		for _, rel := range []string{p.origRel, p.thumbRel} {
			if _, err := os.Stat(filepath.Join(w.app.mediaDir, filepath.FromSlash(rel))); err == nil {
				return fmt.Errorf("%s is still public", rel)
			}
		}
		return nil
	})
	sc.Step(`^the original waits in a holding folder that no page serves$`, func() error {
		p := w.posts["Bad one"]
		orig := filepath.Join(w.app.holdFolder(p.id), "original.jpg")
		if _, err := os.Stat(orig); err != nil {
			return fmt.Errorf("the original is not held: %v", err)
		}
		if strings.HasPrefix(orig, w.app.mediaDir) {
			return fmt.Errorf("the holding folder is under the media path")
		}
		if rec := doAs(t, w.app, nil, "GET", "/media/../photo-holding/"+strconv.FormatInt(p.id, 10)+"/original.jpg", nil); rec.Code == http.StatusOK {
			return fmt.Errorf("a page serves the held original")
		}
		return nil
	})
	sc.Step(`^the visitor is refused$`, func() error {
		if w.code != http.StatusUnauthorized && w.code != http.StatusForbidden {
			return fmt.Errorf("answered %d, want 401 or 403", w.code)
		}
		return nil
	})
	sc.Step(`^nothing of that photo is anywhere on the volume$`, func() error {
		p := w.posts["Bad one"]
		var found []string
		_ = filepath.Walk(w.app.dataDir, func(path string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() && (strings.Contains(path, p.origRel[len("2026/11/"):len("2026/11/")+16]) || strings.Contains(path, "photo-holding/"+strconv.FormatInt(p.id, 10))) {
				found = append(found, path)
			}
			return nil
		})
		if len(found) > 0 {
			return fmt.Errorf("still on the volume: %v", found)
		}
		if _, err := w.app.store.HeldPhoto(p.id); err == nil {
			return fmt.Errorf("the hold row is still there")
		}
		return nil
	})
}

func (w *pmWorld) id(title string) string { return strconv.FormatInt(w.posts[title].id, 10) }

func (w *pmWorld) act(c *http.Cookie, path string, f url.Values) error {
	if f == nil {
		f = url.Values{}
	}
	rec := doAs(w.t, w.app, c, "POST", path, f)
	w.code = rec.Code
	return nil
}

func (w *pmWorld) hasPhoto(title string) error {
	got, err := w.app.store.Post(w.posts[title].id)
	if err != nil {
		return err
	}
	if got.Photo == nil {
		return fmt.Errorf("the post has no photo")
	}
	return nil
}

func (w *pmWorld) media(rel string, want int) error {
	rec := doAs(w.t, w.app, nil, "GET", "/media/"+rel, nil)
	if rec.Code != want {
		return fmt.Errorf("/media/%s answered %d, want %d", rel, rec.Code, want)
	}
	return nil
}

// published is the point each post shows on the ride's map data, by post id.
func (w *pmWorld) published() map[int64][2]float64 {
	rec := doAs(w.t, w.app, w.kris, "GET", "/route.geojson", nil)
	var doc struct {
		Features []struct {
			Geometry struct {
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		w.t.Fatalf("route.geojson: %v", err)
	}
	out := map[int64][2]float64{}
	for _, f := range doc.Features {
		var c []float64
		if f.Properties["kind"] == "post" && json.Unmarshal(f.Geometry.Coordinates, &c) == nil && len(c) == 2 {
			out[int64(f.Properties["id"].(float64))] = [2]float64{c[0], c[1]}
		}
	}
	return out
}
