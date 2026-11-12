package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/public_now_playing.feature. The driver reports a track the
// way the phone does (POST /driver/roadtracks/state with started and title); a
// visitor reads GET /tracks.json and the home page with no cookie at all.

type npWorld struct {
	t     *testing.T
	app   *App
	kris  *http.Cookie
	key   int64
	form  url.Values
	pages []string // every body a visitor read, for the leak check
}

type npTrack struct {
	Track     string   `json:"track"`
	Album     string   `json:"album"`
	Artist    string   `json:"artist"`
	Note      string   `json:"note"`
	StartedAt string   `json:"started_at"`
	Lat       *float64 `json:"lat"`
	Lng       *float64 `json:"lng"`
}

type npDoc struct {
	Now    *npTrack  `json:"now"`
	Last   *npTrack  `json:"last"`
	Tracks []npTrack `json:"tracks"`
}

func initPublicNowPlayingScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &npWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = npWorld{t: t, key: 1760000000000}
		return ctx, nil
	})
	sc.Step(`^the trip is active and the driver is signed in with RoadTracks on$`, func() error {
		w.app = activeTripApp(t)
		w.app.tracks = newRoadtracks(rtKey, newMO(t).srv.URL)
		w.kris = asUser(t, w.app, "kris", roleAdmin)
		return nil
	})
	sc.Step(`^the driver's player reports track "([^"]*)" of album (\d+) "([^"]*)" by "([^"]*)" started$`, func(title, id, album, artist string) error {
		w.key++
		w.form = url.Values{"album_id": {id}, "album": {album}, "artist": {artist}, "track_idx": {"0"}, "position_s": {"0.4"},
			"title": {title}, "year": {"1969"}, "started": {strconv.FormatInt(w.key, 10)}}
		return w.send(w.form)
	})
	sc.Step(`^the driver's player reports the same start again$`, func() error { return w.send(w.form) })
	sc.Step(`^the driver's player posts a resume point for album (\d+) with no start$`, func(id string) error {
		return w.send(url.Values{"album_id": {id}, "album": {"Abbey Road"}, "artist": {"The Beatles"}, "track_idx": {"0"}, "position_s": {"12"}})
	})
	sc.Step(`^Drive Tracks picked album (\d+) "([^"]*)" by "([^"]*)" because "([^"]*)"$`, func(id, album, artist, why string) error {
		n, _ := strconv.Atoi(id)
		return w.app.store.InsertChesterQuery(&ChesterQuery{User: "kris", Ride: w.app.activeSlug(), AskedAt: utcString(time.Now().Add(-time.Minute)),
			TZ: "UTC", Mode: "quick", Preset: chesterDriveTracksPreset, Question: "q", Model: stubModel,
			Music: []chesterMusicCard{{AlbumID: int64(n), Album: album, Artist: artist, Year: 1969, Why: why, Label: "Play"}}})
	})
	sc.Step(`^the driver's newest pinned post is at the exact point ([0-9.]+), (-[0-9.]+) near a private stop$`, func(lat, lng string) error {
		la, _ := strconv.ParseFloat(lat, 64)
		ln, _ := strconv.ParseFloat(lng, 64)
		legs := mustLegs(t, w.app, fixtureSlug)
		return w.app.store.CreatePost(&Post{LegID: legs[len(legs)-1].ID, Kind: "note", OccurredAt: utcString(time.Now()), TZ: "America/Denver",
			Title: "at the door", Lat: &la, Lng: &ln, PinSource: pinBrowser})
	})
	sc.Step(`^that play is (\d+) minutes old$`, func(n int) error {
		_, err := w.app.store.db.Exec(`UPDATE plays SET started_at = ?`, utcString(time.Now().Add(-time.Duration(n)*time.Minute)))
		return err
	})

	sc.Step(`^a visitor not signed in sees "([^"]*)" by "([^"]*)" on "([^"]*)" as now playing$`, func(tr, artist, album string) error {
		d, err := w.read()
		if err != nil {
			return err
		}
		return w.is(d.Now, tr, artist, album, "now")
	})
	sc.Step(`^a visitor not signed in sees "([^"]*)" by "([^"]*)" on "([^"]*)" as last played$`, func(tr, artist, album string) error {
		d, err := w.read()
		if err != nil {
			return err
		}
		if d.Now != nil {
			return fmt.Errorf("an old play is still now: %+v", d.Now)
		}
		return w.is(d.Last, tr, artist, album, "last")
	})
	sc.Step(`^a visitor not signed in sees (\d+) tracks? in the list$`, func(n int) error {
		d, err := w.read()
		if err != nil {
			return err
		}
		if len(d.Tracks) != n {
			return fmt.Errorf("%d tracks, want %d", len(d.Tracks), n)
		}
		return nil
	})
	sc.Step(`^the track "([^"]*)" carries the note "([^"]*)"$`, func(tr, note string) error {
		p, err := w.track(tr)
		if err != nil {
			return err
		}
		if p.Note != note {
			return fmt.Errorf("note %q, want %q", p.Note, note)
		}
		return nil
	})
	sc.Step(`^the track "([^"]*)" carries no note$`, func(tr string) error {
		p, err := w.track(tr)
		if err != nil {
			return err
		}
		if p.Note != "" {
			return fmt.Errorf("an invented note: %q", p.Note)
		}
		return nil
	})
	sc.Step(`^the track "([^"]*)" is published at ([0-9.]+), (-[0-9.]+)$`, func(tr string, lat, lng float64) error {
		p, err := w.track(tr)
		if err != nil {
			return err
		}
		if p.Lat == nil || p.Lng == nil || *p.Lat != lat || *p.Lng != lng {
			return fmt.Errorf("point %v, %v, want %v, %v", p.Lat, p.Lng, lat, lng)
		}
		return nil
	})
	sc.Step(`^the track "([^"]*)" has no point$`, func(tr string) error {
		p, err := w.track(tr)
		if err != nil {
			return err
		}
		if p.Lat != nil || p.Lng != nil {
			return fmt.Errorf("a point where none was known: %v, %v", p.Lat, p.Lng)
		}
		return nil
	})
	sc.Step(`^nothing a visitor can read holds the exact point ([0-9.]+) or (-[0-9.]+)$`, func(lat, lng string) error {
		if _, err := w.read(); err != nil {
			return err
		}
		// The data this feature adds: /tracks.json and the copy of it the home
		// page embeds. (The rest of the page is the existing post pages; the post
		// here was written raw, past the form's own snap, only to feed the play.)
		page := w.visit("/")
		at := strings.Index(page, "window.RIDE_TRACKS")
		if at < 0 {
			return fmt.Errorf("the home page embeds no tracks; this check would pass on nothing")
		}
		embedded := page[at : at+strings.Index(page[at:], "</script>")]
		for _, b := range []string{w.pages[0], embedded} {
			if strings.Contains(b, lat) || strings.Contains(b, lng) {
				return fmt.Errorf("the exact point is readable by a visitor")
			}
		}
		return nil
	})
	sc.Step(`^what a visitor can read holds no key, no library link, no album number and no user name$`, func() error {
		if _, err := w.read(); err != nil {
			return err
		}
		data := strings.ToLower(w.pages[0]) // /tracks.json
		for _, bad := range []string{strings.ToLower(rtKey), "/api/roadtracks", "/album/", "album_id", "user", "kris", "http"} {
			if strings.Contains(data, bad) {
				return fmt.Errorf("/tracks.json holds %q", bad)
			}
		}
		if page := w.visit("/"); strings.Contains(page, rtKey) || strings.Contains(page, "/api/roadtracks") {
			return fmt.Errorf("the home page holds the key or a library address")
		}
		return nil
	})
	sc.Step(`^the home page for a visitor not signed in carries the Currently playing chip and the Listen Inn toggle$`, func() error {
		page := w.visit("/")
		for _, want := range []string{`id="np-chip"`, "Currently playing", `id="np-toggle"`, "Listen Inn", "window.RIDE_TRACKS"} {
			if !strings.Contains(page, want) {
				return fmt.Errorf("the home page has no %q", want)
			}
		}
		return nil
	})
	sc.Step(`^the home page for a visitor not signed in carries neither the chip nor the toggle$`, func() error {
		page := w.visit("/")
		for _, bad := range []string{`id="np-chip"`, "Listen Inn", `id="np-toggle"`, "window.RIDE_TRACKS"} {
			if strings.Contains(page, bad) {
				return fmt.Errorf("the home page of a ride with no plays has %q", bad)
			}
		}
		return nil
	})
}

func (w *npWorld) send(form url.Values) error {
	rec := doAs(w.t, w.app, w.kris, "POST", "/driver/roadtracks/state", form)
	if rec.Code != http.StatusNoContent {
		return fmt.Errorf("the state post answered %d: %s", rec.Code, rec.Body.String())
	}
	return nil
}

// visit reads a page with no cookie and remembers the body.
func (w *npWorld) visit(path string) string {
	rec := doAs(w.t, w.app, nil, "GET", path, nil)
	w.pages = append(w.pages, rec.Body.String())
	return rec.Body.String()
}

func (w *npWorld) read() (*npDoc, error) {
	w.pages = nil
	body := w.visit("/tracks.json")
	var d npDoc
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		return nil, fmt.Errorf("/tracks.json is not JSON: %v: %s", err, body)
	}
	return &d, nil
}

func (w *npWorld) is(p *npTrack, tr, artist, album, which string) error {
	if p == nil {
		return fmt.Errorf("no %s", which)
	}
	if p.Track != tr || p.Artist != artist || p.Album != album {
		return fmt.Errorf("%s is %+v", which, *p)
	}
	return nil
}

func (w *npWorld) track(title string) (*npTrack, error) {
	d, err := w.read()
	if err != nil {
		return nil, err
	}
	for i := range d.Tracks {
		if d.Tracks[i].Track == title {
			return &d.Tracks[i], nil
		}
	}
	return nil, fmt.Errorf("no track %q in the list", title)
}
