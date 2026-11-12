package main

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/continuous_music_and_one_card.feature. The server-side
// scenarios read the page a real request gets from the real mux. The player,
// the frame script and the nest run under node (continuous_node_test.go).
func initContinuousMusicScenarios(t *testing.T, sc *godog.ScenarioContext) {
	var (
		app      *App
		kris     *http.Cookie
		page     string
		rec      *httptest.ResponseRecorder
		tracksOn bool
	)
	var (
		audioRe  = regexp.MustCompile(`<audio[ >]`)
		frameRe  = regexp.MustCompile(`<iframe[^>]*>`)
		srcRe    = regexp.MustCompile(`src="([^"]*)"`)
		idRe     = regexp.MustCompile(`\sid="([^"]+)"`)
		cardRe   = regexp.MustCompile(`class="drive-card"`)
		resumeRe = regexp.MustCompile(`id="rt-resume"[^>]*data-pos="([^"]*)"`)
		crumbRe  = regexp.MustCompile(`(?s)<nav class="crumbs"`)
		h1Re     = regexp.MustCompile(`<h1[ >]`)
	)
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		tracksOn = true
		return ctx, nil
	})
	build := func() {
		if app != nil {
			return
		}
		app = chesterApp(t, newAPIStub(t))
		if tracksOn {
			app.tracks = newRoadtracks(rtKey, newMO(t).srv.URL)
		}
		kris = asUser(t, app, "kris", roleEditor)
	}
	get := func(c *http.Cookie, method, target string, hdr map[string]string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if c != nil {
			req.AddCookie(c)
		}
		w := httptest.NewRecorder()
		app.routes().ServeHTTP(w, req)
		return w
	}
	open := func(c *http.Cookie, path string, hdr map[string]string) {
		build()
		rec = get(c, "GET", path, hdr, nil)
		page = rec.Body.String()
		if rec.Code != 200 {
			t.Fatalf("%s answered %d", path, rec.Code)
		}
	}
	start := func() error {
		build()
		rv, err := app.loadRide()
		if err != nil {
			return err
		}
		return app.store.SetArrived(tripStart(rv.Legs).ID, time.Now(), nil)
	}
	sc.Step(`^a continuous-music trip with a music library$`, func() error { app = nil; tracksOn = true; return nil })
	sc.Step(`^the music library is off$`, func() error { app = nil; tracksOn = false; return nil })
	sc.Step(`^the continuous-music trip-keeper Kris is signed in$`, func() error { return nil })
	sc.Step(`^the continuous-music drive is started$`, start)
	sc.Step(`^the drive has not started$`, func() error { build(); return nil })
	sc.Step(`^Kris opens the page "([^"]*)"$`, func(p string) error { open(kris0(build, &kris), p, nil); return nil })
	sc.Step(`^Kris opens the page "([^"]*)" as a frame navigation$`, func(p string) error {
		open(kris0(build, &kris), p, map[string]string{"Sec-Fetch-Dest": "iframe"})
		return nil
	})
	sc.Step(`^Kris loads "([^"]*)" as a whole page$`, func(p string) error {
		build()
		rec = get(kris, "GET", p, map[string]string{"Sec-Fetch-Dest": "document"}, nil)
		page = rec.Body.String()
		return nil
	})
	sc.Step(`^a visitor opens the page "([^"]*)"$`, func(p string) error {
		build()
		rec = get(nil, "GET", p, nil, nil)
		page = rec.Body.String()
		return nil
	})
	sc.Step(`^Kris has a continuous-music resume point for "([^"]*)" at (\d+) seconds$`, func(album string, s int) error {
		build()
		return app.store.SavePlayerState("kris", 42, album, "The Beatles", 1, float64(s), time.Now())
	})

	sc.Step(`^the page is the player shell with exactly one audio element and one frame$`, func() error {
		if n := len(audioRe.FindAllString(page, -1)); n != 1 {
			return fmt.Errorf("%d audio elements", n)
		}
		if n := len(frameRe.FindAllString(page, -1)); n != 1 {
			return fmt.Errorf("%d frames", n)
		}
		return nil
	})
	sc.Step(`^the frame shows "([^"]*)" as an embedded page$`, func(want string) error {
		m := srcRe.FindStringSubmatch(frameRe.FindString(page))
		if m == nil {
			return fmt.Errorf("no frame src")
		}
		u, err := url.Parse(html.UnescapeString(m[1]))
		if err != nil {
			return err
		}
		w, _ := url.Parse(want)
		q := u.Query()
		if q.Get("embed") != "1" || u.Path != w.Path {
			return fmt.Errorf("frame src %q for %q", m[1], want)
		}
		q.Del("embed")
		if q.Encode() != w.Query().Encode() {
			return fmt.Errorf("frame src %q lost the query of %q", m[1], want)
		}
		return nil
	})
	sc.Step(`^no element id appears twice in the shell$`, func() error {
		seen := map[string]bool{}
		for _, m := range idRe.FindAllStringSubmatch(page, -1) {
			if seen[m[1]] {
				return fmt.Errorf("id %q appears twice", m[1])
			}
			seen[m[1]] = true
		}
		return nil
	})
	sc.Step(`^the page keeps its banner, menu, breadcrumb and one h1$`, func() error {
		for _, s := range []string{`class="masthead"`, `class="mast-nav"`} {
			if !strings.Contains(page, s) {
				return fmt.Errorf("the page lost %s", s)
			}
		}
		if n := len(crumbRe.FindAllString(page, -1)); n != 1 {
			return fmt.Errorf("%d breadcrumbs", n)
		}
		if n := len(h1Re.FindAllString(page, -1)); n != 1 {
			return fmt.Errorf("%d h1", n)
		}
		return nil
	})
	sc.Step(`^the page has no audio element, no Drive Tracks card and no frame$`, func() error {
		if audioRe.MatchString(page) || cardRe.MatchString(page) || frameRe.MatchString(page) || strings.Contains(page, `id="rt-player"`) {
			return fmt.Errorf("the page carries the card, the audio element or a frame")
		}
		return nil
	})
	sc.Step(`^the page carries the frame script$`, func() error {
		if !strings.Contains(page, `/static/js/frame.js`) {
			return fmt.Errorf("no frame.js")
		}
		return nil
	})
	sc.Step(`^Kris posts an empty post form from the frame$`, func() error {
		build()
		rec = get(kris, "POST", "/driver/post?embed=1", map[string]string{"Sec-Fetch-Dest": "iframe"}, url.Values{"x": {"1"}})
		return nil
	})
	sc.Step(`^Kris posts an empty post form from a whole page$`, func() error {
		build()
		rec = get(kris, "POST", "/driver/post", nil, url.Values{"x": {"1"}})
		return nil
	})
	sc.Step(`^the answer redirects to a path that carries embed=1$`, func() error {
		loc := rec.Header().Get("Location")
		if rec.Code < 300 || rec.Code > 399 || !strings.Contains(loc, "embed=1") || !strings.HasPrefix(loc, "/driver/post") {
			return fmt.Errorf("%d to %q", rec.Code, loc)
		}
		return nil
	})
	sc.Step(`^the answer redirects to a path that does not carry embed=1$`, func() error {
		loc := rec.Header().Get("Location")
		if rec.Code < 300 || rec.Code > 399 || strings.Contains(loc, "embed") || !strings.HasPrefix(loc, "/driver/post") {
			return fmt.Errorf("%d to %q", rec.Code, loc)
		}
		return nil
	})
	sc.Step(`^Kris signs out$`, func() error {
		build()
		rec = get(kris, "POST", "/driver/logout", nil, url.Values{})
		return nil
	})
	sc.Step(`^the answer redirects to "([^"]*)" with no embed=1$`, func(want string) error {
		if loc := rec.Header().Get("Location"); loc != want {
			return fmt.Errorf("redirected to %q", loc)
		}
		return nil
	})
	sc.Step(`^the page has no player shell, no audio element and no frame$`, func() error {
		if audioRe.MatchString(page) || frameRe.MatchString(page) {
			return fmt.Errorf("a visitor got the shell")
		}
		return nil
	})
	sc.Step(`^the player shell is switched off$`, func() error {
		build()
		app.shellOff = true
		return nil
	})
	sc.Step(`^the page has no player shell and no frame$`, func() error {
		if frameRe.MatchString(page) {
			return fmt.Errorf("the page is a shell")
		}
		return nil
	})
	sc.Step(`^every kind of answer says it may be framed by its own origin only$`, func() error {
		build()
		for _, c := range []*http.Cookie{kris, nil} {
			for _, p := range []string{"/", "/plan?embed=1", "/driver/login", "/healthz", "/static/css/app.css"} {
				w := get(c, "GET", p, nil, nil)
				if w.Header().Get("X-Frame-Options") != "SAMEORIGIN" || w.Header().Get("Content-Security-Policy") != "frame-ancestors 'self'" {
					return fmt.Errorf("%s: X-Frame-Options %q, CSP %q", p, w.Header().Get("X-Frame-Options"), w.Header().Get("Content-Security-Policy"))
				}
			}
		}
		return nil
	})
	sc.Step(`^the Sign out form targets the top window$`, func() error {
		if !regexp.MustCompile(`<form[^>]*action="/driver/logout"[^>]*target="_top"|<form[^>]*target="_top"[^>]*action="/driver/logout"`).MatchString(page) {
			return fmt.Errorf("the Sign out form does not target _top")
		}
		return nil
	})
	sc.Step(`^the answer redirects to the sign-in page whose next is "([^"]*)"$`, func(next string) error {
		loc := rec.Header().Get("Location")
		u, err := url.Parse(loc)
		if rec.Code != 302 || err != nil || u.Path != "/driver/login" || u.Query().Get("next") != next {
			return fmt.Errorf("%d to %q", rec.Code, loc)
		}
		return nil
	})
	sc.Step(`^the sign-in page asked for from the frame carries the break-out$`, func() error {
		build()
		w := get(nil, "GET", "/driver/login?next=%2Fdriver&embed=1", nil, nil)
		if !strings.Contains(w.Body.String(), `/static/js/frame.js`) || !strings.Contains(w.Body.String(), `data-page="login"`) {
			return fmt.Errorf("the sign-in page cannot break out of a frame")
		}
		return nil
	})

	sc.Step(`^the shell has one Drive Tracks card with a heading, art, title, artist, track, Play and Next$`, func() error {
		if n := len(cardRe.FindAllString(page, -1)); n != 1 {
			return fmt.Errorf("%d cards", n)
		}
		if !regexp.MustCompile(`(?s)<h2 class="drive-card-title"[^>]*>.*?Drive Tracks.*?</h2>`).MatchString(page) {
			return fmt.Errorf("the card's title is not its h2 header")
		}
		for _, id := range []string{"rt-art", "rt-title", "rt-artist", "rt-track", "rt-play", "rt-next"} {
			if strings.Count(page, `id="`+id+`"`) != 1 {
				return fmt.Errorf("id %s is not there exactly once", id)
			}
		}
		return nil
	})
	sc.Step(`^Ask Chester for an album is a labelled button inside that card with its icon showing$`, func() error {
		m := regexp.MustCompile(`(?s)<button [^>]*id="hud-drivetracks"[^>]*>(.*?)</button>`).FindStringSubmatch(page)
		if m == nil || !strings.Contains(m[0], "type=\"button\"") || !strings.Contains(m[1], "Ask Chester for an album") || !strings.Contains(m[1], `aria-hidden="true"`) {
			return fmt.Errorf("the Ask button is %v", m)
		}
		if strings.Contains(m[0], "btn-dark") || strings.Contains(m[0], "btn-primary") {
			return fmt.Errorf("the Ask button is a dark or filled button that hides its icon: %s", m[0])
		}
		at, end := strings.Index(page, `class="drive-card"`), strings.Index(page, "</section>")
		if i := strings.Index(page, `id="hud-drivetracks"`); i < at || i > end {
			return fmt.Errorf("the Ask button is outside the card")
		}
		return nil
	})
	sc.Step(`^there is no second Drive Tracks card or strip$`, func() error {
		if strings.Contains(page, `class="drive-strip"`) || strings.Contains(page, "hud-player") || strings.Count(page, `class="drive-card-title"`) != 1 {
			return fmt.Errorf("a second card or the old strip is on the page")
		}
		return nil
	})
	sc.Step(`^the card says how many songs are ready$`, func() error {
		if !strings.Contains(page, `id="rt-nest"`) {
			return fmt.Errorf("no ready readout")
		}
		return nil
	})
	sc.Step(`^the card opens collapsed with a header that expands it and the Charter icon$`, func() error {
		b, err := os.ReadFile("templates/base.html")
		if err != nil {
			return err
		}
		for _, want := range []string{`id="rt-toggle"`, `aria-expanded="false"`, `aria-controls="rt-card-body"`, `id="rt-card-body" hidden`, `/static/img/chester.svg`} {
			if !strings.Contains(string(b), want) {
				return fmt.Errorf("the card has no %s", want)
			}
		}
		return nil
	})
	sc.Step(`^the card's controls are at least 44 px tall and the card is docked full width$`, func() error {
		css := readFile(t, "static/css/app.css")
		for _, want := range []string{".drive-card .btn", "min-height: 44px", ".rt-dock", "width: 100%"} {
			if !strings.Contains(css, want) {
				return fmt.Errorf("app.css lacks %q", want)
			}
		}
		return nil
	})
	sc.Step(`^the card offers a Resume of "([^"]*)" at (\d+) seconds$`, func(album string, s int) error {
		m := resumeRe.FindStringSubmatch(page)
		if m == nil || !strings.Contains(page, "Resume "+album) || m[1] != fmt.Sprint(s) {
			return fmt.Errorf("no Resume of %s at %d", album, s)
		}
		return nil
	})
	initContinuousNodeScenarios(t, sc)
}

// kris0 builds the app once and hands back Kris's cookie.
func kris0(build func(), c **http.Cookie) *http.Cookie { build(); return *c }
