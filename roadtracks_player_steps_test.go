package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// initRoadtracksPlayerScenarios binds features/roadtracks_card_player.feature.
func initRoadtracksPlayerScenarios(t *testing.T, sc *godog.ScenarioContext) {
	var (
		app    *App
		kris   *http.Cookie
		maria  *http.Cookie
		answer struct {
			code int
			loc  string
			body string
		}
		seen []string // every body this scenario saw, for the key check
	)
	note := func(code int, loc, body string) {
		answer.code, answer.loc, answer.body = code, loc, body
		seen = append(seen, body)
	}
	do := func(c *http.Cookie, method, target string, form url.Values) {
		w := doAs(t, app, c, method, target, form)
		note(w.Code, w.Header().Get("Location"), w.Body.String())
	}
	cookieOf := func(who string) *http.Cookie {
		if who == "maria" {
			return maria
		}
		return kris
	}
	resumeOf := func(c *http.Cookie) (map[string]any, error) {
		w := doAs(t, app, c, "GET", "/driver/hud.json", nil)
		seen = append(seen, w.Body.String())
		if w.Code != 200 {
			return nil, fmt.Errorf("hud.json answered %d", w.Code)
		}
		var h map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &h); err != nil {
			return nil, err
		}
		r, _ := h["resume"].(map[string]any)
		return r, nil
	}
	type track struct {
		Src *string `json:"src"`
	}
	album := func() (art string, tracks []track, err error) {
		var o struct {
			Art    string  `json:"art"`
			Tracks []track `json:"tracks"`
		}
		if err = json.Unmarshal([]byte(answer.body), &o); err != nil {
			return "", nil, fmt.Errorf("not json (%d): %s", answer.code, answer.body)
		}
		return o.Art, o.Tracks, nil
	}
	trackN := func(n int) (track, error) {
		_, ts, err := album()
		if err != nil {
			return track{}, err
		}
		if n < 1 || n > len(ts) {
			return track{}, fmt.Errorf("no track %d in %s", n, answer.body)
		}
		return ts[n-1], nil
	}
	post := func(c *http.Cookie, id, idx, pos string) {
		do(c, "POST", "/driver/roadtracks/state", url.Values{"album_id": {id}, "album": {"Abbey Road"}, "artist": {"The Beatles"}, "track_idx": {idx}, "position_s": {pos}})
	}

	sc.Step(`^the player library is on with the phone address "([^"]*)"$`, func(link string) error {
		mo := newMO(t)
		app, kris = tracksApp(t, mo)
		app.tracks.linkBase = link
		maria = asUser(t, app, "maria", roleAdmin)
		seen = nil
		return nil
	})
	sc.Step(`^the driver asks for album (\d+)$`, func(id string) error {
		do(kris, "GET", "/driver/roadtracks/album/"+id, nil)
		return nil
	})
	sc.Step(`^nobody signed in asks for album (\d+)$`, func(id string) error {
		do(nil, "GET", "/driver/roadtracks/album/"+id, nil)
		return nil
	})
	sc.Step(`^nobody signed in posts a resume point$`, func() error {
		post(nil, "42", "1", "5")
		return nil
	})
	sc.Step(`^the answer is ok$`, func() error {
		if answer.code != 200 {
			return fmt.Errorf("answered %d: %s", answer.code, answer.body)
		}
		return nil
	})
	sc.Step(`^the answer is a plain error$`, func() error {
		var o map[string]string
		if err := json.Unmarshal([]byte(answer.body), &o); err != nil || answer.code < 400 || len(o) != 1 || o["error"] == "" || len(strings.Fields(o["error"])) > 6 {
			return fmt.Errorf("not one plain error word: %d %q", answer.code, answer.body)
		}
		return nil
	})
	sc.Step(`^the answer redirects to the login page$`, func() error {
		if answer.code != http.StatusFound || !strings.HasPrefix(answer.loc, "/driver/login") {
			return fmt.Errorf("%d %q", answer.code, answer.loc)
		}
		return nil
	})
	sc.Step(`^the answer is refused$`, func() error {
		if answer.code != http.StatusUnauthorized && answer.code != http.StatusBadRequest && answer.code != http.StatusForbidden {
			return fmt.Errorf("answered %d: %s", answer.code, answer.body)
		}
		return nil
	})
	sc.Step(`^track (\d+) has the address "([^"]*)"$`, func(n int, want string) error {
		tr, err := trackN(n)
		if err != nil {
			return err
		}
		if tr.Src == nil || *tr.Src != want {
			return fmt.Errorf("track %d src: %v, want %s", n, tr.Src, want)
		}
		return nil
	})
	sc.Step(`^track (\d+) has no address$`, func(n int) error {
		tr, err := trackN(n)
		if err != nil {
			return err
		}
		if tr.Src != nil {
			return fmt.Errorf("track %d src kept: %s", n, *tr.Src)
		}
		return nil
	})
	sc.Step(`^the album art is "([^"]*)"$`, func(want string) error {
		art, _, err := album()
		if err != nil {
			return err
		}
		if art != want {
			return fmt.Errorf("art %q, want %q", art, want)
		}
		return nil
	})
	sc.Step(`^(kris|maria) saves a resume point for album (\d+) at track (\d+) and ([0-9.]+) seconds$`, func(who, id, idx, pos string) error {
		post(cookieOf(who), id, idx, pos)
		if answer.code >= 300 {
			return fmt.Errorf("save answered %d: %s", answer.code, answer.body)
		}
		return nil
	})
	sc.Step(`^(kris|maria) posts a resume point with position "([^"]*)"$`, func(who, pos string) error {
		post(cookieOf(who), "42", "1", pos)
		return nil
	})
	sc.Step(`^(kris|maria) sees a resume point for album (\d+) at track (\d+) and ([0-9.]+) seconds$`, func(who string, id, idx int, pos float64) error {
		r, err := resumeOf(cookieOf(who))
		if err != nil {
			return err
		}
		if r == nil {
			return fmt.Errorf("no resume point")
		}
		if int(r["album_id"].(float64)) != id || int(r["track_idx"].(float64)) != idx || r["position_s"].(float64) != pos {
			return fmt.Errorf("resume: %v", r)
		}
		return nil
	})
	sc.Step(`^(kris|maria) sees no resume point$`, func(who string) error {
		r, err := resumeOf(cookieOf(who))
		if err != nil {
			return err
		}
		if r != nil {
			return fmt.Errorf("unexpected resume: %v", r)
		}
		return nil
	})
	sc.Step(`^that resume point is (\d+) hours old$`, func(h int) error {
		_, err := app.store.db.Exec(`UPDATE player_state SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now', ?)`, "-"+strconv.Itoa(h)+" hours")
		return err
	})
	sc.Step(`^the driver page for kris offers Resume$`, func() error {
		w := doAs(t, app, kris, "GET", "/driver", nil)
		if !strings.Contains(w.Body.String(), `id="rt-resume"`) {
			return fmt.Errorf("no Resume button")
		}
		return nil
	})
	sc.Step(`^the driver page for kris offers no Resume$`, func() error {
		w := doAs(t, app, kris, "GET", "/driver", nil)
		if strings.Contains(w.Body.String(), `id="rt-resume"`) {
			return fmt.Errorf("a Resume button for stale state")
		}
		return nil
	})
	sc.Step(`^kris is in the "([^"]*)" mode$`, func(mode string) error {
		do(kris, "POST", "/driver/freelance/mode", url.Values{"mode": {mode}})
		return nil
	})
	tagRe := regexp.MustCompile(`<(/?)section\b[^>]*>`)
	sc.Step(`^the player is on the driver page outside every mode section$`, func() error {
		page := doAs(t, app, kris, "GET", "/driver", nil).Body.String()
		at := strings.Index(page, `id="rt-player"`)
		if at < 0 {
			return fmt.Errorf("no player on the page")
		}
		hud := strings.Index(page, `<section class="hud" id="hud"`)
		if hud < 0 || hud > at {
			return fmt.Errorf("player is not inside the hud")
		}
		depth, roads := 0, 0
		for _, m := range tagRe.FindAllStringSubmatchIndex(page[hud:at], -1) {
			tag := page[hud+m[0] : hud+m[1]]
			if page[hud+m[2]:hud+m[3]] == "/" {
				depth--
			} else {
				depth++
				if strings.Contains(tag, "hud-road") {
					roads++
				}
			}
		}
		// The enclosing #hud is depth 1; the player's own tag opens at the same level, and it now sits above the road section.
		if depth != 1 || roads != 0 {
			return fmt.Errorf("player is nested inside a mode section (depth %d, mode sections before it %d)", depth, roads)
		}
		return nil
	})
	sc.Step(`^the key is in no answer$`, func() error {
		do(kris, "GET", "/driver", nil)
		do(kris, "GET", "/driver/roadtracks/album/42", nil)
		do(kris, "GET", "/driver/roadtracks/album/99", nil)
		for _, b := range seen {
			if strings.Contains(b, rtKey) {
				return fmt.Errorf("the key is in an answer")
			}
		}
		return nil
	})
}
