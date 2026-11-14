package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/roadtracks_favourite_without_a_loaded_album.feature. The
// phone's request is sent exactly as static/js/player.js builds it (no album
// when nothing is loaded), and the line shown is the page's own markStatus run
// under node.
type favWorld struct {
	t     *testing.T
	app   *App
	kris  *http.Cookie
	code  int
	saved string
	kind  string
}

func initFavouriteScenarios(t *testing.T, sc *godog.ScenarioContext) {
	f := &favWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*f = favWorld{t: t}
		return ctx, nil
	})
	sc.Step(`^the trip-keeper has a clean RoadTracks card$`, func() error {
		f.app, f.kris = tracksApp(t, newMO(t))
		return nil
	})
	sc.Step(`^the server holds a resume point in "([^"]*)" at track (\d+)$`, func(album string, n int) error {
		w := doAs(t, f.app, f.kris, "POST", "/driver/roadtracks/state", url.Values{"album_id": {"42"}, "album": {album}, "artist": {"The Beatles"}, "track_idx": {fmt.Sprint(n)}, "position_s": {"61.5"}})
		if w.Code >= 300 {
			return fmt.Errorf("state answered %d: %s", w.Code, w.Body.String())
		}
		return nil
	})
	sc.Step(`^the trip-keeper (favourites|marks) with nothing loaded$`, func(verb string) error {
		return f.send(map[string]string{"favourites": "favourite", "marks": "bookmark"}[verb], url.Values{})
	})
	sc.Step(`^the trip-keeper favourites the loaded track "([^"]*)" of "([^"]*)" at track (\d+)$`, func(title, album string, n int) error {
		return f.send("favourite", url.Values{"album_id": {"7"}, "album": {album}, "artist": {"B"}, "track_idx": {fmt.Sprint(n)}, "position_s": {"12.3"}, "title": {title}})
	})
	sc.Step(`^one (favourite|bookmark) is saved for "([^"]*)" at track (\d+)$`, f.savedOne)
	sc.Step(`^nothing is saved$`, func() error {
		ms, err := f.app.store.TrackMarks("kris")
		if err != nil || len(ms) != 0 {
			return fmt.Errorf("marks %v, err %v", ms, err)
		}
		return nil
	})
	sc.Step(`^the reply names "([^"]*)"$`, func(want string) error {
		if f.saved != want {
			return fmt.Errorf("the reply names %q, want %q", f.saved, want)
		}
		return nil
	})
	sc.Step(`^the line shown reads "([^"]*)"$`, func(want string) error {
		in := map[string]any{"kind": f.kind, "code": f.code}
		if f.code == 200 {
			in["saved"] = f.saved
		}
		o, err := runNode(t, "static/js/mark_harness.mjs", in)
		if err != nil {
			return err
		}
		if o["status"] != want {
			return fmt.Errorf("the line reads %q, want %q", o["status"], want)
		}
		return nil
	})
}

func (f *favWorld) send(kind string, form url.Values) error {
	form.Set("kind", kind)
	w := doAs(f.t, f.app, f.kris, "POST", "/driver/roadtracks/mark", form)
	f.kind, f.code, f.saved = kind, w.Code, ""
	if w.Code == http.StatusOK {
		var o map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &o); err != nil {
			return fmt.Errorf("reply %q: %v", w.Body.String(), err)
		}
		f.saved = o["saved"]
	}
	return nil
}

func (f *favWorld) savedOne(kind, album string, n int) error {
	ms, err := f.app.store.TrackMarks("kris")
	if err != nil {
		return err
	}
	if len(ms) != 1 || ms[0].Kind != kind || ms[0].Album != album {
		return fmt.Errorf("saved marks %+v, want one %s for %s", ms, kind, album)
	}
	var idx int
	if err := f.app.store.db.QueryRow(`SELECT track_idx FROM track_marks WHERE user='kris'`).Scan(&idx); err != nil || idx != n {
		return fmt.Errorf("track_idx %d (%v), want %d", idx, err, n)
	}
	return nil
}
