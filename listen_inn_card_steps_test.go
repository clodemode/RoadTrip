package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/listen_inn_floating_card.feature and
// features/listen_inn_song_link.feature. They share the world of
// public_now_playing.feature: the driver reports a track the way the phone
// does, and a visitor reads pages with no cookie at all.

type licWorld struct {
	*npWorld
	rec   *httptest.ResponseRecorder
	noted string
}

func initListenInnCardScenarios(t *testing.T, sc *godog.ScenarioContext, np *npWorld) {
	w := &licWorld{npWorld: np}
	node := func(script string) (string, error) {
		cmd := exec.Command("node", "-e", script)
		cmd.Env = append(os.Environ(), "TZ=UTC")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("node: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out)), nil
	}
	idOf := func(title string) (string, error) {
		p, err := w.track(title)
		if err != nil {
			return "", err
		}
		if p.ID == "" {
			return "", fmt.Errorf("the track %q has no public name", title)
		}
		return p.ID, nil
	}
	open := func(c *http.Cookie, path string) { w.rec = doAs(t, w.app, c, "GET", path, nil) }
	body := func() string { return w.rec.Body.String() }
	// noPlayer: the card names the track and carries nothing that could sound.
	noPlayer := func(page, track string) error {
		if !regexp.MustCompile(`(?s)id="np-ready"[^>]*>.*?` + regexp.QuoteMeta(track)).MatchString(page) {
			return fmt.Errorf("the card does not name %q", track)
		}
		for _, bad := range []string{"<audio", `id="np-listen"`, "/static/js/listen.js"} {
			if strings.Contains(page, bad) {
				return fmt.Errorf("the page holds %q", bad)
			}
		}
		return nil
	}

	// --- the floating card
	sc.Step(`^the Ride page for a visitor carries the Listen Inn card docked after the page content$`, func() error {
		page := w.visit("/")
		at := strings.Index(page, `<section class="tune-card listen-card listen-dock" id="np"`)
		if at < 0 {
			return fmt.Errorf("the Ride page has no docked Listen Inn card")
		}
		if end := strings.Index(page, "</main>"); end < 0 || at < end {
			return fmt.Errorf("the card is inside the page content, not docked after it")
		}
		if strings.Contains(page, `<aside class="np"`) {
			return fmt.Errorf("the old map control is still on the page")
		}
		return nil
	})
	sc.Step(`^the card's title is its header button and the card starts closed$`, func() error {
		page := w.visit("/")
		if !regexp.MustCompile(`(?s)<h2 class="tune-card-title"[^>]*>\s*<button type="button" class="rt-toggle" id="np-toggle" aria-expanded="false" aria-controls="np-body">.*?Listen Inn.*?</button>`).MatchString(page) {
			return fmt.Errorf("the title is not a closed header button")
		}
		if !strings.Contains(page, `id="np-body" hidden`) {
			return fmt.Errorf("the card's body does not start hidden")
		}
		return nil
	})
	sc.Step(`^the card holds one audio element and one Play button$`, func() error {
		page := w.visit("/")
		if a, b := strings.Count(page, "<audio"), strings.Count(page, `id="np-listen"`); a != 1 || b != 1 {
			return fmt.Errorf("%d audio elements and %d Play buttons", a, b)
		}
		return nil
	})
	var mapOut map[string]any
	runMap := func() error {
		if mapOut != nil {
			return nil
		}
		play := map[string]any{"id": "a1", "track": "T", "artist": "R", "album": "A", "started_at": "2026-11-12T12:00:00Z", "lat": 40.0, "lng": -100.0}
		o, err := runNode(t, "static/js/map_harness.mjs", map[string]any{
			"data":   map[string]any{"legs": []any{}, "tracks": []any{}, "checkpoints": []any{}, "sights": []any{}, "posts": []any{}},
			"size":   map[string]int{"w": 375, "h": 380},
			"tracks": map[string]any{"now": nil, "last": play, "tracks": []any{play}}})
		mapOut = o
		return err
	}
	sc.Step(`^the map script adds no Listen Inn control to the map$`, func() error {
		mapOut = nil
		if err := runMap(); err != nil {
			return err
		}
		if n, ok := mapOut["customControls"].(float64); !ok || n != 0 {
			return fmt.Errorf("the map script added %v control(s) of its own", mapOut["customControls"])
		}
		b, err := os.ReadFile("static/js/map.js")
		if err != nil {
			return err
		}
		for _, bad := range []string{"np-toggle", "np-panel", "getElementById('np')"} {
			if strings.Contains(string(b), bad) {
				return fmt.Errorf("map.js still reaches for %s", bad)
			}
		}
		return nil
	})
	sc.Step(`^the map still draws a music note for a play with a point$`, func() error {
		if err := runMap(); err != nil {
			return err
		}
		ms, _ := mapOut["markers"].([]any)
		for _, m := range ms {
			if mm, ok := m.(map[string]any); ok && strings.Contains(fmt.Sprint(mm["cls"]), "np-pin") {
				return nil
			}
		}
		return fmt.Errorf("no music note among %d markers", len(ms))
	})
	cardDef := func(name string) (string, error) {
		b, err := templateFS.ReadFile("templates/base.html")
		if err != nil {
			return "", err
		}
		s := string(b)
		at := strings.Index(s, `{{define "`+name+`"}}`)
		if at < 0 {
			return "", fmt.Errorf("base.html defines no %s", name)
		}
		end := strings.Index(s[at:], "</section>")
		if end < 0 {
			return "", fmt.Errorf("%s has no section", name)
		}
		return s[at : at+end], nil
	}
	sc.Step(`^the Listen Inn card and the driver's music card both show the music mark from this site's own files$`, func() error {
		for _, name := range []string{"drive_card", "listen_card"} {
			def, err := cardDef(name)
			if err != nil {
				return err
			}
			if !strings.Contains(def, `src="/static/img/charter.svg?v=`) {
				return fmt.Errorf("%s does not show the mark", name)
			}
		}
		b, err := staticFS.ReadFile("static/img/charter.svg")
		if err != nil {
			return fmt.Errorf("the mark is not in this site's own files: %v", err)
		}
		if svg := string(b); !strings.Contains(svg, "<svg") || regexp.MustCompile(`(?i)href\s*=|url\(|<script|<image`).MatchString(svg) {
			return fmt.Errorf("the mark is not a plain drawing that loads nothing")
		}
		return nil
	})
	sc.Step(`^the driver's music card no longer shows the helper's icon$`, func() error {
		def, err := cardDef("drive_card")
		if err != nil {
			return err
		}
		if strings.Contains(def, "chester.svg") {
			return fmt.Errorf("the driver's music card still shows the helper's icon")
		}
		return nil
	})
	sc.Step(`^one style rule dresses both cards$`, func() error {
		b, err := staticFS.ReadFile("static/css/app.css")
		if err != nil {
			return err
		}
		if !regexp.MustCompile(`\.drive-card,\s*\.listen-card\s*\{[^}]*border[^}]*\}`).Match(b) {
			return fmt.Errorf("no one rule gives both cards their frame")
		}
		return nil
	})
	sc.Step(`^no page prints the mark's internal name$`, func() error {
		pages := shipped(t, templateFS, "templates", ".html")
		requireFiles(t, pages, "templates/base.html", "templates/index.html")
		comment := regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`)
		for path, s := range pages {
			s = strings.ReplaceAll(comment.ReplaceAllString(s, ""), "/static/img/charter.svg", "")
			if strings.Contains(strings.ToLower(s), "charter") {
				return fmt.Errorf("%s prints the mark's internal name", path)
			}
		}
		return nil
	})
	focus := func(a, b, c, asked string) (string, error) {
		// The page's list is newest first, as /tracks.json sends it.
		return node(fmt.Sprintf(`const m=require('./static/js/listeninn.js');console.log(m.focusId([{id:%q},{id:%q},{id:%q}],%q))`, c, b, a, asked))
	}
	sc.Step(`^with the songs "([^"]*)", "([^"]*)" and "([^"]*)" played in that order the card opens on "([^"]*)"$`, func(a, b, c, want string) error {
		got, err := focus(a, b, c, "")
		if err == nil && got != want {
			err = fmt.Errorf("the card opens on %q, want %q", got, want)
		}
		return err
	})
	sc.Step(`^with the songs "([^"]*)", "([^"]*)" and "([^"]*)" played in that order and "([^"]*)" asked for the card opens on "([^"]*)"$`, func(a, b, c, asked, want string) error {
		got, err := focus(a, b, c, asked)
		if err == nil && got != want {
			err = fmt.Errorf("the card opens on %q, want %q", got, want)
		}
		return err
	})
	sc.Step(`^a song that started at "([^"]*)" in "([^"]*)" reads "([^"]*)" and never "([^"]*)"$`, func(iso, tz, want, never string) error {
		got, err := node(fmt.Sprintf(`const m=require('./static/js/nowplaying.js');console.log(m.fmtWhen(%q,%q))`, iso, tz))
		if err != nil {
			return err
		}
		got = strings.ReplaceAll(got, " ", " ")
		if !strings.Contains(got, want) || strings.Contains(got, never) {
			return fmt.Errorf("the time reads %q", got)
		}
		return nil
	})
	phoneBlocks := func() ([]string, error) {
		b, err := staticFS.ReadFile("static/css/app.css")
		if err != nil {
			return nil, err
		}
		var out []string
		for _, part := range strings.Split(string(b), "@media (max-width: 575px)")[1:] {
			// A one-line rule closes on its own line; a block closes at the next "\n}".
			line := part
			if nl := strings.Index(part, "\n"); nl > 0 {
				line = part[:nl]
			}
			if strings.Count(line, "{") == strings.Count(line, "}") {
				part = line
			} else if end := strings.Index(part, "\n}"); end > 0 {
				part = part[:end]
			}
			out = append(out, part)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("app.css has no phone rule: this check would pass on nothing")
		}
		return out, nil
	}
	sc.Step(`^at phone width the open song list is capped at (\d+)vh or less$`, func(max int) error {
		blocks, err := phoneBlocks()
		if err != nil {
			return err
		}
		for _, blk := range blocks {
			if m := regexp.MustCompile(`\.np-panel\s*\{[^}]*max-height:\s*([0-9.]+)vh`).FindStringSubmatch(blk); m != nil {
				if v, _ := strconv.ParseFloat(m[1], 64); v > float64(max) {
					return fmt.Errorf("the phone rule caps the list at %svh", m[1])
				}
				return nil
			}
		}
		return fmt.Errorf("no phone rule caps the open list")
	})
	sc.Step(`^the page keeps room under its content for the docked card$`, func() error {
		b, err := staticFS.ReadFile("static/css/app.css")
		if err != nil {
			return err
		}
		if !regexp.MustCompile(`body:has\(\.listen-dock\)\s*\{[^}]*padding-bottom`).Match(b) {
			return fmt.Errorf("the docked card would sit on the end of the page")
		}
		if !regexp.MustCompile(`\.listen-dock\s*\{[^}]*position:\s*fixed[^}]*bottom`).Match(b) {
			return fmt.Errorf("the card is not docked at the bottom")
		}
		return nil
	})
	sc.Step(`^the Ride page inside the driver's player frame carries no Listen Inn card$`, func() error {
		page := doAs(t, w.app, w.kris, "GET", "/?embed=1", nil).Body.String()
		for _, bad := range []string{`id="np"`, `id="np-toggle"`, "listeninn.js", "<audio", `class="drive-card"`} {
			if strings.Contains(page, bad) {
				return fmt.Errorf("the framed page holds %q", bad)
			}
		}
		return nil
	})

	// --- the song link
	sc.Step(`^a visitor follows the link "([^"]*)"$`, func(path string) error { open(nil, path); return nil })
	sc.Step(`^a visitor opens the song link of "([^"]*)"$`, func(title string) error {
		id, err := idOf(title)
		if err == nil {
			open(nil, "/listeninn/song/"+id)
		}
		return err
	})
	sc.Step(`^a visitor notes the song link of "([^"]*)"$`, func(title string) error {
		id, err := idOf(title)
		w.noted = "/listeninn/song/" + id
		return err
	})
	sc.Step(`^a visitor opens the noted song link$`, func() error { open(nil, w.noted); return nil })
	sc.Step(`^every play belongs to another ride$`, func() error {
		_, err := w.app.store.db.Exec(`UPDATE plays SET ride_slug = 'another-ride'`)
		return err
	})
	sc.Step(`^the driver opens the song link of "([^"]*)"$`, func(title string) error {
		id, err := idOf(title)
		if err == nil {
			w.noted = "/listeninn/song/" + id
			open(w.kris, w.noted)
		}
		return err
	})
	sc.Step(`^the driver has left the start$`, func() error {
		rv, err := w.app.loadRide()
		if err != nil {
			return err
		}
		return w.app.store.SetArrived(tripStart(rv.Legs).ID, time.Now(), nil)
	})
	sc.Step(`^the site's public address is known$`, func() error { w.app.siteURL = "http://example.com"; return nil })
	sc.Step(`^visitors may not hear the music$`, func() error { w.app.listenOff = true; return nil })
	sc.Step(`^the whole site is private$`, func() error { w.app.sitePrivate = true; return nil })

	sc.Step(`^the link answers 200 and the card is open$`, func() error {
		if w.rec.Code != http.StatusOK {
			return fmt.Errorf("the answer is %d", w.rec.Code)
		}
		page := body()
		for _, want := range []string{`id="np-toggle" aria-expanded="true"`, `data-open="1"`, `<div id="np-body">`} {
			if !strings.Contains(page, want) {
				return fmt.Errorf("the card is not open: no %s", want)
			}
		}
		return nil
	})
	sc.Step(`^the link answers (\d+)$`, func(code int) error {
		if w.rec.Code != code {
			return fmt.Errorf("the answer is %d, want %d", w.rec.Code, code)
		}
		return nil
	})
	sc.Step(`^the Play button is armed on "([^"]*)"$`, func(title string) error {
		id, err := idOf(title)
		if err != nil {
			return err
		}
		if !regexp.MustCompile(`<button [^>]*id="np-listen"[^>]*data-id="` + id + `"`).MatchString(body()) {
			return fmt.Errorf("the Play button is not armed on %q", title)
		}
		return nil
	})
	sc.Step(`^the card says "([^"]*)" by "([^"]*)" is ready to play$`, func(track, artist string) error {
		m := regexp.MustCompile(`(?s)<p class="np-ready" id="np-ready">(.*?)</p>`).FindStringSubmatch(body())
		if m == nil || !strings.Contains(m[1], "Ready to play") || !strings.Contains(m[1], track) || !strings.Contains(m[1], artist) {
			return fmt.Errorf("the card does not say the song is ready: %v", m)
		}
		return nil
	})
	sc.Step(`^nothing on the page starts sound by itself$`, func() error {
		if strings.Contains(strings.ToLower(body()), "autoplay") {
			return fmt.Errorf("the page asks for sound without a tap")
		}
		return nil
	})
	sc.Step(`^the page's own address in its head is the song link$`, func() error {
		id, err := idOf("Come Together")
		if err != nil {
			return err
		}
		link := "http://example.com/listeninn/song/" + id
		for _, want := range []string{`<link rel="canonical" href="` + link + `">`, `<meta property="og:url" content="` + link + `">`} {
			if !strings.Contains(body(), want) {
				return fmt.Errorf("the head has no %s", want)
			}
		}
		return nil
	})
	sc.Step(`^the preview title and description name "([^"]*)" and "([^"]*)"$`, func(track, artist string) error {
		for _, prop := range []string{"og:title", "og:description"} {
			m := regexp.MustCompile(`<meta property="` + prop + `" content="([^"]*)">`).FindStringSubmatch(body())
			if m == nil || !strings.Contains(m[1], track) || !strings.Contains(m[1], artist) {
				return fmt.Errorf("%s does not name the song: %v", prop, m)
			}
		}
		return nil
	})
	sc.Step(`^the page says the song is not on this trip$`, func() error {
		if !regexp.MustCompile(`<p class="np-msg" id="np-msg" role="status">That song is not on this trip`).MatchString(body()) {
			return fmt.Errorf("the page does not say the song is not on this trip")
		}
		return nil
	})
	sc.Step(`^the visitor is sent to "([^"]*)"$`, func(to string) error {
		if w.rec.Code != http.StatusSeeOther || w.rec.Header().Get("Location") != to {
			return fmt.Errorf("the answer is %d to %q", w.rec.Code, w.rec.Header().Get("Location"))
		}
		return nil
	})
	sc.Step(`^the visitor is sent to the sign-in page$`, func() error {
		if w.rec.Code != http.StatusFound || !strings.HasPrefix(w.rec.Header().Get("Location"), "/driver/login?next=") {
			return fmt.Errorf("the answer is %d to %q", w.rec.Code, w.rec.Header().Get("Location"))
		}
		return nil
	})
	sc.Step(`^the card names "([^"]*)" with no audio element and no Play button$`, func(track string) error { return noPlayer(body(), track) })
	sc.Step(`^the driver gets the player shell framing that song link$`, func() error {
		page := body()
		if w.rec.Code != http.StatusOK || !strings.Contains(page, `id="rt-frame"`) || !strings.Contains(page, `src="`+w.noted+`?embed=1"`) {
			return fmt.Errorf("the answer (%d) is not the shell framing %s: %.600s", w.rec.Code, w.noted, page)
		}
		if n := strings.Count(page, `class="drive-card"`); n != 1 {
			return fmt.Errorf("the shell has %d music cards", n)
		}
		if strings.Contains(page, `id="np-audio"`) {
			return fmt.Errorf("the shell holds a second player")
		}
		return nil
	})
	sc.Step(`^the framed page names "([^"]*)" with no audio element and no Play button$`, func(track string) error {
		page := doAs(t, w.app, w.kris, "GET", w.noted+"?embed=1", nil).Body.String()
		if strings.Contains(page, `class="drive-card"`) {
			return fmt.Errorf("the framed page holds the driver's music card")
		}
		return noPlayer(page, track)
	})
	rowOf := func(id string) (string, error) {
		return node(fmt.Sprintf(`const m=require('./static/js/nowplaying.js');console.log(m.panelHtml([{id:%q,track:'T',album:'A',artist:'R',started_at:'2026-11-12T12:00:00Z'}],String,false,true))`, id))
	}
	sc.Step(`^a list row for the song "([^"]*)" links to "([^"]*)"$`, func(id, href string) error {
		h, err := rowOf(id)
		if err == nil && !strings.Contains(h, `<a class="np-link" href="`+href+`"`) {
			err = fmt.Errorf("the row has no link to %s: %s", href, h)
		}
		return err
	})
	sc.Step(`^a list row with no name carries no link$`, func() error {
		h, err := rowOf("")
		if err == nil && strings.Contains(h, "/listeninn/song/") {
			err = fmt.Errorf("a row with no name carries a link: %s", h)
		}
		return err
	})
}
