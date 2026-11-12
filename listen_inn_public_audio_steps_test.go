package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/listen_inn_public_audio.feature. The library is the moStub
// of roadtracks_test.go; a visitor carries no cookie at all.

type liWorld struct {
	t     *testing.T
	app   *App
	mo    *moStub
	kris  *http.Cookie
	key   int64
	seen  []string // everything a visitor was sent: bodies, locations, headers
	last  *httptest.ResponseRecorder
	ids   map[string]string
	other bool
}

func initListenInnScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &liWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = liWorld{t: t, key: 1760000000000, ids: map[string]string{}}
		return ctx, nil
	})
	sc.Step(`^the trip is active, the driver is signed in and RoadTracks is on$`, func() error {
		w.app = activeTripApp(t)
		w.mo = newMO(t)
		w.app.tracks = newRoadtracks(rtKey, w.mo.srv.URL)
		w.kris = asUser(t, w.app, "kris", roleAdmin)
		return nil
	})
	sc.Step(`^public listening is switched off$`, func() error { w.app.listenOff = true; return nil })
	sc.Step(`^the library is failing$`, func() error { w.mo.mu.Lock(); w.mo.status = 503; w.mo.mu.Unlock(); return nil })
	sc.Step(`^the driver's player reports track "([^"]*)" at index (\d+) of album (\d+) "([^"]*)" by "([^"]*)" started$`, func(title, idx, id, album, artist string) error {
		w.key++
		rec := doAs(t, w.app, w.kris, "POST", "/driver/roadtracks/state", url.Values{"album_id": {id}, "album": {album}, "artist": {artist},
			"track_idx": {idx}, "position_s": {"0.4"}, "title": {title}, "year": {"1969"}, "started": {strconv.FormatInt(w.key, 10)}})
		if rec.Code != http.StatusNoContent {
			return fmt.Errorf("the state post answered %d", rec.Code)
		}
		return nil
	})
	sc.Step(`^that play belongs to another ride$`, func() error {
		_, err := w.app.store.db.Exec(`UPDATE plays SET ride_slug = 'some-other-ride'`)
		return err
	})
	sc.Step(`^a visitor not signed in asks for the audio of the play "([^"]*)"$`, func(title string) error {
		id := title
		if n, ok := w.nameOf(title); ok {
			id = n
		}
		w.ask(id)
		return nil
	})
	sc.Step(`^one address asks for the audio of the play "([^"]*)" (\d+) times$`, func(title string, n int) error {
		id, ok := w.nameOf(title)
		if !ok {
			return fmt.Errorf("no play %q", title)
		}
		for i := 0; i < n; i++ {
			w.ask(id)
		}
		return nil
	})
	sc.Step(`^the visitor is sent to the library's address for album 42 track 2$`, func() error {
		if w.last.Code != http.StatusFound {
			return fmt.Errorf("answered %d: %s", w.last.Code, w.last.Body.String())
		}
		if loc := w.last.Header().Get("Location"); loc != "https://cdn.example.test/t2.mp3?sig=y" {
			return fmt.Errorf("sent to %q", loc)
		}
		return nil
	})
	sc.Step(`^the library was asked about album 42 only$`, func() error {
		w.mo.mu.Lock()
		defer w.mo.mu.Unlock()
		if w.mo.albums != 1 {
			return fmt.Errorf("the library was asked %d times", w.mo.albums)
		}
		return nil
	})
	sc.Step(`^the library was never asked$`, func() error {
		w.mo.mu.Lock()
		defer w.mo.mu.Unlock()
		if w.mo.albums != 0 {
			return fmt.Errorf("the library was asked %d times", w.mo.albums)
		}
		return nil
	})
	sc.Step(`^the visitor is told (\d+)$`, func(code int) error {
		if w.last.Code != code {
			return fmt.Errorf("answered %d, want %d: %s", w.last.Code, code, w.last.Body.String())
		}
		return nil
	})
	sc.Step(`^the visitor is told (\d+) with a plain sentence$`, func(code int) error {
		if w.last.Code != code {
			return fmt.Errorf("answered %d, want %d", w.last.Code, code)
		}
		return plain(w.last)
	})
	sc.Step(`^the last answer is 429 with a plain sentence$`, func() error {
		if w.last.Code != http.StatusTooManyRequests {
			return fmt.Errorf("answered %d, want 429", w.last.Code)
		}
		return plain(w.last)
	})
	sc.Step(`^/tracks.json says listening is off$`, func() error {
		d, _, err := w.tracksDoc()
		if err != nil {
			return err
		}
		if d.Listen {
			return fmt.Errorf("/tracks.json says listening is on")
		}
		return nil
	})
	sc.Step(`^/tracks.json says listening is on and every row has an opaque name$`, func() error {
		d, raw, err := w.tracksDoc()
		if err != nil {
			return err
		}
		if !d.Listen || len(d.Tracks) != 2 {
			return fmt.Errorf("listen=%v rows=%d", d.Listen, len(d.Tracks))
		}
		for _, p := range d.Tracks {
			if len(p.ID) != 16 || strings.ContainsAny(p.ID, "ghijklmnopqrstuvwxyz") {
				return fmt.Errorf("row %q has name %q", p.Track, p.ID)
			}
		}
		if strings.Contains(raw, "album_id") || strings.Contains(raw, "42") && !strings.Contains(raw, "started_at") {
			return fmt.Errorf("the album number may be in /tracks.json")
		}
		return nil
	})
	sc.Step(`^the home page for a visitor carries no listen control$`, func() error {
		page := w.visit("/")
		for _, bad := range []string{`id="np-audio"`, "/static/js/listen.js"} {
			if strings.Contains(page, bad) {
				return fmt.Errorf("the page holds %q with listening off", bad)
			}
		}
		if !strings.Contains(page, `"listen":false`) {
			return fmt.Errorf("the embedded tracks do not say listening is off")
		}
		return nil
	})
	sc.Step(`^the home page for a visitor carries the audio element and the shared script$`, func() error {
		page := w.visit("/")
		for _, want := range []string{`id="np-audio"`, "/static/js/listen.js", `"listen":true`} {
			if !strings.Contains(page, want) {
				return fmt.Errorf("the page has no %q", want)
			}
		}
		return nil
	})
	sc.Step(`^nothing a visitor was sent holds the library key or a library album address$`, func() error {
		w.visit("/")
		w.visit("/tracks.json")
		if len(w.seen) < 3 {
			return fmt.Errorf("only %d answers seen; this check would pass on nothing", len(w.seen))
		}
		for _, s := range w.seen {
			l := strings.ToLower(s)
			for _, bad := range []string{strings.ToLower(rtKey), "bearer", "/api/roadtracks", w.mo.srv.URL + "/api"} {
				if strings.Contains(l, bad) {
					return fmt.Errorf("a visitor was sent %q", bad)
				}
			}
		}
		return nil
	})
}

func plain(rec *httptest.ResponseRecorder) error {
	b := strings.TrimSpace(rec.Body.String())
	if b == "" || strings.HasPrefix(b, "{") || strings.Contains(strings.ToLower(b), "<html") || !strings.HasSuffix(b, ".") {
		return fmt.Errorf("not a plain sentence: %q", b)
	}
	return nil
}

func (w *liWorld) visit(path string) string {
	rec := doAs(w.t, w.app, nil, "GET", path, nil)
	w.note(rec)
	return rec.Body.String()
}

func (w *liWorld) note(rec *httptest.ResponseRecorder) {
	w.last = rec
	h, _ := json.Marshal(rec.Header())
	w.seen = append(w.seen, rec.Body.String()+string(h))
}

func (w *liWorld) ask(id string) { w.note(doAs(w.t, w.app, nil, "GET", "/tracks/"+id+"/audio", nil)) }

func (w *liWorld) tracksDoc() (*liDoc, string, error) {
	body := w.visit("/tracks.json")
	var d liDoc
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		return nil, "", err
	}
	return &d, body, nil
}

type liDoc struct {
	Listen bool `json:"listen"`
	Tracks []struct {
		ID    string `json:"id"`
		Track string `json:"track"`
	} `json:"tracks"`
}

// nameOf is the opaque name of a play, read as a visitor would, or straight
// from the table when the play is on another ride (a visitor cannot see it).
func (w *liWorld) nameOf(title string) (string, bool) {
	var id string
	if err := w.app.store.db.QueryRow(`SELECT pub_id FROM plays WHERE title = ?`, title).Scan(&id); err == nil {
		return id, true
	}
	return "", false
}
