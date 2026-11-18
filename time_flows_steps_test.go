package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/time_flows_oldest_to_newest.feature. Everything is made
// through the store at fixed times on the fixture trip's first day, so the
// order a page shows is the order of the times and nothing else.

type flowWorld struct {
	t      *testing.T
	app    *App
	kris   *http.Cookie
	body   string
	code   int
	posts  map[string]int64 // title -> post id
	photos int
}

var (
	flowDay     = time.Date(2030, 5, 1, 0, 0, 0, 0, time.UTC)
	flowStripRe = regexp.MustCompile(`(?s)<nav class="latest"[^>]*>.*?</nav>`)
	flowTileRe  = func(kind string) *regexp.Regexp {
		return regexp.MustCompile(`(?s)<a class="latest-tile latest-` + kind + `"[^>]*>.*?</a>`)
	}
	flowHrefRe   = regexp.MustCompile(`href="([^"]*)"`)
	flowPrevRe   = regexp.MustCompile(`<a class="photo-prev" href="/photo/(\d+)"`)
	flowNextRe   = regexp.MustCompile(`<a class="photo-next" href="/photo/(\d+)"`)
	flowTimeline = regexp.MustCompile(`(?s)<section class="timeline">.*?</section>`)
)

func initTimeFlowScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &flowWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = flowWorld{t: t}
		return ctx, nil
	})
	at := func(hh, mm int, nextDay bool) time.Time {
		d := flowDay
		if nextDay {
			d = d.AddDate(0, 0, 1)
		}
		return d.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute)
	}
	fresh := func() {
		w.app = activeTripApp(t)
		w.kris = asUser(t, w.app, "kris", roleAdmin)
		w.posts = map[string]int64{}
	}
	sc.Step(`^a trip under way that left Alder at (\d+):(\d+) UTC on its first day$`, func(hh, mm int) error {
		fresh()
		cp, err := w.cp("Alder")
		if err != nil {
			return err
		}
		return w.app.store.SetArrived(cp.ID, at(hh, mm, false), nil)
	})
	sc.Step(`^a trip that has not started and holds nothing$`, func() error { fresh(); return nil })
	sc.Step(`^a (photo|note) post "([^"]*)" at ([A-Z][A-Za-z ]+) at (\d+):(\d+) UTC$`, func(kind, title, place string, hh, mm int) error {
		return w.post(kind, title, place, at(hh, mm, false), false)
	})
	sc.Step(`^a note post "([^"]*)" with no pin at (\d+):(\d+) UTC$`, func(title string, hh, mm int) error {
		return w.post("note", title, "", at(hh, mm, false), false)
	})
	sc.Step(`^a note post "([^"]*)" that hides its location at (\d+):(\d+) UTC$`, func(title string, hh, mm int) error {
		return w.post("note", title, "Dogwood", at(hh, mm, false), true)
	})
	sc.Step(`^the song "([^"]*)" by "([^"]*)" played at (\d+):(\d+) UTC$`, func(title, artist string, hh, mm int) error {
		return w.play(title, artist, at(hh, mm, false))
	})
	sc.Step(`^the song "([^"]*)" by "([^"]*)" started a minute ago$`, func(title, artist string) error {
		return w.play(title, artist, time.Now().Add(-time.Minute))
	})
	sc.Step(`^the car arrived at ([A-Z][A-Za-z ]+) at (\d+):(\d+) UTC the next day$`, func(place string, hh, mm int) error {
		cp, err := w.cp(place)
		if err != nil {
			return err
		}
		return w.app.store.SetArrived(cp.ID, at(hh, mm, true), nil)
	})
	sc.Step(`^the trip is finished at ([A-Z][A-Za-z ]+) at (\d+):(\d+) UTC the next day$`, func(place string, hh, mm int) error {
		rv, err := w.app.loadRide()
		if err != nil {
			return err
		}
		cps := rideCheckpoints(rv.Legs)
		when := at(hh, mm, true)
		for _, c := range cps[1 : len(cps)-1] {
			if c.ArrivedAt == "" && c.SkippedAt == "" {
				if err := w.app.store.SetSkipped(c.ID, when, "missed"); err != nil {
					return err
				}
			}
		}
		last := cps[len(cps)-1]
		if last.Name != place {
			return fmt.Errorf("the trip ends at %s, not %s", last.Name, place)
		}
		if err := w.app.store.SetArrived(last.ID, when, nil); err != nil {
			return err
		}
		if !w.app.rideIsFinished() {
			return fmt.Errorf("the ride is not finished")
		}
		return nil
	})
	sc.Step(`^the trip-keeper favourited "([^"]*)" and then "([^"]*)"$`, func(a, b string) error {
		for i, title := range []string{a, b} {
			if err := w.app.store.AddTrackMark("kris", "favourite", int64(10+i), "Album", "Artist", i, title, 0, time.Now()); err != nil {
				return err
			}
		}
		return nil
	})

	sc.Step(`^a visitor opens the Ride page$`, func() error { return w.get(nil, "/") })
	sc.Step(`^the trip-keeper opens the Ride page$`, func() error { return w.get(w.kris, "/") })
	sc.Step(`^a visitor opens the first leg's timeline$`, func() error {
		rv, err := w.app.loadRide()
		if err != nil {
			return err
		}
		return w.get(nil, "/legs/"+rv.Legs[0].Slug)
	})
	sc.Step(`^a visitor opens the photo gallery$`, func() error { return w.get(nil, "/photos") })
	sc.Step(`^the trip-keeper opens the Remove photos page$`, func() error { return w.get(w.kris, "/driver/posts") })
	sc.Step(`^the trip-keeper reads the post census$`, func() error { return w.get(w.kris, "/driver/posts.json") })
	sc.Step(`^the trip-keeper opens the plan page$`, func() error { return w.get(w.kris, "/plan") })
	sc.Step(`^a visitor opens the viewer of "([^"]*)"$`, func(title string) error {
		return w.get(nil, fmt.Sprintf("/photo/%d", w.posts[title]))
	})

	sc.Step(`^the latest strip is drawn before the map$`, func() error {
		s, m := strings.Index(w.body, `<nav class="latest"`), strings.Index(w.body, `id="map"`)
		if s < 0 || m < 0 || s > m {
			return fmt.Errorf("strip at %d, map at %d", s, m)
		}
		return nil
	})
	sc.Step(`^the point tile names "([^"]*)" at "([^"]*)"$`, func(place, when string) error {
		tile, err := w.tile("point")
		if err != nil {
			return err
		}
		if !strings.Contains(tile, ">"+place+"<") || !strings.Contains(tile, when) {
			return fmt.Errorf("the point tile is %s", tile)
		}
		return nil
	})
	sc.Step(`^the song tile reads "([^"]*)" and names "([^"]*)" by "([^"]*)"$`, func(label, title, artist string) error {
		tile, err := w.tile("song")
		if err != nil {
			return err
		}
		for _, want := range []string{">" + label + "<", ">" + title + "<", artist} {
			if !strings.Contains(tile, want) {
				return fmt.Errorf("the song tile lacks %q: %s", want, tile)
			}
		}
		return nil
	})
	sc.Step(`^the song tile links to the Listen Inn$`, func() error { return w.tileHref("song", "/listeninn") })
	sc.Step(`^the photo tile shows the photo of "([^"]*)" and links to its viewer$`, func(title string) error {
		tile, err := w.tile("photo")
		if err != nil {
			return err
		}
		if !strings.Contains(tile, `<img src="/media/`+flowThumb(title)+`"`) {
			return fmt.Errorf("the photo tile shows another photo: %s", tile)
		}
		return w.tileHref("photo", fmt.Sprintf("/photo/%d", w.posts[title]))
	})
	sc.Step(`^the latest strip carries no coordinates$`, func() error {
		strip := flowStripRe.FindString(w.body)
		if strip == "" {
			return fmt.Errorf("no strip")
		}
		if m := coordLikeRe.FindString(strip); m != "" {
			return fmt.Errorf("the strip carries %q", m)
		}
		return nil
	})
	sc.Step(`^the point tile links to the post "([^"]*)" on the timeline$`, func(title string) error {
		return w.tileHref("point", fmt.Sprintf("#post-%d", w.posts[title]))
	})
	sc.Step(`^that post is on the page under the same anchor$`, func() error {
		tile, _ := w.tile("point")
		id := strings.TrimPrefix(flowHrefRe.FindStringSubmatch(tile)[1], "#")
		if !strings.Contains(w.body, `id="`+id+`"`) {
			return fmt.Errorf("nothing on the page has id %q", id)
		}
		return nil
	})
	sc.Step(`^the point tile links to the stop "([^"]*)" in the checkpoint list$`, func(place string) error {
		cp, err := w.cp(place)
		if err != nil {
			return err
		}
		return w.tileHref("point", fmt.Sprintf("#cp-%d", cp.ID))
	})
	sc.Step(`^that stop is on the page under the same anchor$`, func() error {
		tile, _ := w.tile("point")
		id := strings.TrimPrefix(flowHrefRe.FindStringSubmatch(tile)[1], "#")
		if !regexp.MustCompile(`<li class="cp[^"]*" id="` + id + `"`).MatchString(w.body) {
			return fmt.Errorf("no checkpoint row has id %q", id)
		}
		return nil
	})
	sc.Step(`^the timeline lists ((?:"[^"]*"(?:, )?)+) in that order$`, func(list string) error {
		return inOrder(flowTimeline.FindString(w.body), "<h3>%s</h3>", list)
	})
	sc.Step(`^the gallery lists ((?:"[^"]*"(?:, )?)+) in that order$`, func(list string) error {
		return inOrder(w.body, `alt="%s"`, list)
	})
	sc.Step(`^the saved music lists ((?:"[^"]*"(?:, )?)+) in that order$`, func(list string) error {
		return inOrder(w.body, "<b>%s</b>", list)
	})
	sc.Step(`^the census lists ((?:"[^"]*"(?:, )?)+) in that order$`, func(list string) error {
		var doc struct {
			Posts []struct {
				Title string `json:"title"`
			} `json:"posts"`
		}
		if err := json.Unmarshal([]byte(w.body), &doc); err != nil {
			return fmt.Errorf("the census is not JSON: %v", err)
		}
		var got []string
		for _, p := range doc.Posts {
			got = append(got, p.Title)
		}
		if want := quoted(list); strings.Join(got, "|") != strings.Join(want, "|") {
			return fmt.Errorf("the census reads %v, want %v", got, want)
		}
		return nil
	})
	sc.Step(`^Previous leads to the photo of "([^"]*)"$`, func(title string) error { return w.leads(flowPrevRe, "Previous", title) })
	sc.Step(`^Next leads to the photo of "([^"]*)"$`, func(title string) error { return w.leads(flowNextRe, "Next", title) })
	sc.Step(`^the viewer offers no Previous$`, func() error { return w.lacks(`class="photo-prev"`) })
	sc.Step(`^the viewer offers no Next$`, func() error { return w.lacks(`class="photo-next"`) })
	sc.Step(`^the page draws no now-playing chip$`, func() error { return w.lacks(`id="np-chip"`) })
	sc.Step(`^the page draws no latest strip$`, func() error { return w.lacks(`class="latest`) })
	// Kris, 2026-11-18, ruling 8: the Listen Inn card replaces the Last played tile.
	sc.Step(`^the Listen Inn card stands in the top band and the strip draws no song tile$`, func() error {
		band := regexp.MustCompile(`(?s)<div class="top-band">.*?<div id="map"`).FindString(w.body)
		if !strings.Contains(band, `id="np"`) {
			return fmt.Errorf("the top band holds no Listen Inn card")
		}
		return w.lacks(`latest-song`)
	})
	sc.Step(`^the page draws no photo tile$`, func() error { return w.lacks(`latest-photo`) })
	sc.Step(`^the page draws no point tile and no photo tile$`, func() error {
		if err := w.lacks(`latest-point`); err != nil {
			return err
		}
		return w.lacks(`latest-photo`)
	})
}

func flowThumb(title string) string {
	return "thumb-" + strings.ReplaceAll(strings.ToLower(title), " ", "-") + ".jpg"
}

func quoted(list string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(list, -1) {
		out = append(out, m[1])
	}
	return out
}

// inOrder reads the titles out of body in the shape a page writes them and
// wants each one present, once, after the one before it.
func inOrder(body, shape, list string) error {
	if body == "" {
		return fmt.Errorf("the page has no such list")
	}
	last := -1
	for _, title := range quoted(list) {
		needle := fmt.Sprintf(shape, title)
		i := strings.Index(body, needle)
		if i < 0 {
			return fmt.Errorf("%q is not in the list", title)
		}
		if i < last {
			return fmt.Errorf("%q comes before the one that should precede it", title)
		}
		last = i
	}
	return nil
}

func (w *flowWorld) cp(name string) (*Checkpoint, error) {
	rv, err := w.app.loadRide()
	if err != nil {
		return nil, err
	}
	cps := rideCheckpoints(rv.Legs)
	for i := range cps {
		if cps[i].Name == name {
			return &cps[i], nil
		}
	}
	return nil, fmt.Errorf("the trip has no stop called %s", name)
}

// post makes one post at a fixed time. place "" is a post with no pin; a
// place pins it to that stop, the way a check-in post is pinned.
func (w *flowWorld) post(kind, title, place string, when time.Time, hide bool) error {
	rv, err := w.app.loadRide()
	if err != nil {
		return err
	}
	p := &Post{LegID: rv.Legs[0].ID, Kind: kind, OccurredAt: utcString(when), TZ: "America/Chicago", Title: title, HideLocation: hide}
	if place != "" {
		cp, err := w.cp(place)
		if err != nil {
			return err
		}
		p.LegID, p.CheckpointID, p.TZ = cp.LegID, cp.ID, cp.TZ
		if !hide {
			la, ln := cp.Lat, cp.Lng
			p.Lat, p.Lng, p.PinSource = &la, &ln, "checkpoint"
		}
	}
	if kind == "photo" {
		w.photos++
		ph := &Photo{Path: "full-" + flowThumb(title), ThumbPath: flowThumb(title), TakenAt: utcString(when), TZ: p.TZ,
			Width: 40, Height: 30, SHA256: fmt.Sprintf("flow-%d", w.photos)}
		if err := w.app.store.CreatePhoto(ph); err != nil {
			return err
		}
		p.PhotoID = ph.ID
	}
	if err := w.app.store.CreatePost(p); err != nil {
		return err
	}
	w.posts[title] = p.ID
	return nil
}

func (w *flowWorld) play(title, artist string, when time.Time) error {
	return w.app.store.AddPlay(Play{Ride: w.app.activeSlug(), User: "kris", AlbumID: 7, Album: "An Album", Artist: artist,
		Title: title, StartedAt: utcString(when), TZ: "America/Denver", StartKey: when.UnixMilli()})
}

func (w *flowWorld) get(c *http.Cookie, path string) error {
	rec := doAs(w.t, w.app, c, "GET", path, nil)
	w.code, w.body = rec.Code, rec.Body.String()
	if w.code != http.StatusOK {
		return fmt.Errorf("%s answered %d", path, w.code)
	}
	return nil
}

func (w *flowWorld) tile(kind string) (string, error) {
	strip := flowStripRe.FindString(w.body)
	if strip == "" {
		return "", fmt.Errorf("the page has no latest strip")
	}
	tile := flowTileRe(kind).FindString(strip)
	if tile == "" {
		return "", fmt.Errorf("the strip has no %s tile", kind)
	}
	return tile, nil
}

func (w *flowWorld) tileHref(kind, want string) error {
	tile, err := w.tile(kind)
	if err != nil {
		return err
	}
	if m := flowHrefRe.FindStringSubmatch(tile); m == nil || m[1] != want {
		return fmt.Errorf("the %s tile links to %v, want %s", kind, m, want)
	}
	return nil
}

func (w *flowWorld) leads(re *regexp.Regexp, name, title string) error {
	m := re.FindStringSubmatch(w.body)
	if m == nil || m[1] != fmt.Sprint(w.posts[title]) {
		return fmt.Errorf("%s leads to %v, want the photo of %q (/photo/%d)", name, m, title, w.posts[title])
	}
	return nil
}

func (w *flowWorld) lacks(s string) error {
	if strings.Contains(w.body, s) {
		return fmt.Errorf("the page still has %q", s)
	}
	return nil
}
