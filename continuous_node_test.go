package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// runNode feeds a harness its JSON on stdin and returns what it printed.
func runNode(t *testing.T, script string, in any) (map[string]any, error) {
	t.Helper()
	b, _ := json.Marshal(in)
	cmd := exec.Command("node", script)
	cmd.Stdin = bytes.NewReader(b)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("node %s: %v: %s", script, err, se.String())
	}
	var out map[string]any
	if err := json.Unmarshal(so.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("harness answered %q", so.String())
	}
	return out, nil
}

func cmStrs(v any) []string {
	var o []string
	for _, x := range v.([]any) {
		o = append(o, fmt.Sprint(x))
	}
	return o
}

// initContinuousNodeScenarios binds the node-run scenarios of
// features/continuous_music_and_one_card.feature: the shell's player and nest
// (static/js/player.js) and the frame script (static/js/frame.js).
func initContinuousNodeScenarios(t *testing.T, sc *godog.ScenarioContext) {
	type world struct {
		tracks    int
		actions   []map[string]any
		mode      string
		ceiling   int
		out       map[string]any
		audioMode string
		albumMode string
		times     map[string]int
		resume    map[string]any
	}
	w := &world{}
	reset := func() { *w = world{tracks: 4, mode: "ok"} }
	reset()
	run := func() error {
		if w.out != nil {
			return nil
		}
		in := map[string]any{"tracks": w.tracks, "actions": w.actions, "fetchMode": w.mode,
			"audioMode": w.audioMode, "albumMode": w.albumMode}
		if w.times != nil {
			in["times"] = w.times
		}
		if w.resume != nil {
			in["resume"] = w.resume
		}
		if w.ceiling > 0 {
			in["ceiling"], in["bytes"] = w.ceiling, 1000
		}
		o, err := runNode(t, "static/js/continuous_harness.mjs", in)
		w.out = o
		return err
	}
	act := func(a map[string]any) { w.actions = append(w.actions, a); w.out = nil }
	playing := func(n int) error { reset(); w.tracks = n; act(map[string]any{"do": "open", "album": 42}); return nil }
	sc.Step(`^an album of (\d+) tracks is playing in the shell$`, func(n int) error {
		keepCfg := *w
		reset()
		w.mode, w.ceiling = keepCfg.mode, keepCfg.ceiling
		w.tracks = n
		act(map[string]any{"do": "open", "album": 42})
		return nil
	})
	_ = playing
	sc.Step(`^the shell is idle$`, func() error { reset(); return nil })
	sc.Step(`^the shell has not finished downloading$`, func() error { reset(); w.mode = "hang"; return nil })
	sc.Step(`^the library's storage refuses cross-origin downloads$`, func() error { reset(); w.mode = "cors"; return nil })
	sc.Step(`^the library's first addresses have expired$`, func() error { reset(); w.mode = "expired"; return nil })
	sc.Step(`^the memory ceiling is (\d+) bytes and each song is (\d+) bytes$`, func(c, _ int) error { reset(); w.ceiling = c; return nil })
	sc.Step(`^an album of (\d+) tracks is started$`, func(n int) error { w.tracks = n; act(map[string]any{"do": "open", "album": 42}); return nil })
	sc.Step(`^another album is started$`, func() error { act(map[string]any{"do": "open", "album": 43}); return nil })
	sc.Step(`^the frame loads "([^"]*)"$`, func(u string) error {
		path, rest, _ := strings.Cut(u, "?")
		search, hash := "", ""
		if rest != "" {
			search = "?" + rest
		} else if i := strings.Index(u, "#"); i >= 0 {
			path, hash = u[:i], u[i:]
		}
		if i := strings.Index(search, "#"); i >= 0 {
			search, hash = search[:i], search[i:]
		}
		act(map[string]any{"do": "frameLoad", "path": path, "search": search, "hash": hash})
		return nil
	})
	sc.Step(`^a tap in the frame starts album (\d+)$`, func(id int) error { act(map[string]any{"do": "frameTap", "album": id}); return nil })
	sc.Step(`^the last track ends$`, func() error { act(map[string]any{"do": "ended", "times": w.tracks}); return nil })
	sc.Step(`^the first track ends$`, func() error { act(map[string]any{"do": "ended"}); return nil })
	sc.Step(`^the listener presses Pause$`, func() error { act(map[string]any{"do": "pause"}); return nil })
	sc.Step(`^the listener signs out$`, func() error { act(map[string]any{"do": "signOut"}); return nil })

	num := func(k string) int { v, _ := w.out[k].(float64); return int(v) }
	srcIs := func(album string, i int) string { return fmt.Sprintf("https://r2.example.test/%s%d?sig=1", album, i) }
	sc.Step(`^the frame loading a new page does not touch the audio$`, func() error {
		if err := run(); err != nil {
			return err
		}
		fl := w.out["frameLoads"].([]any)
		if len(fl) == 0 {
			return fmt.Errorf("no frame load happened")
		}
		for _, x := range fl {
			m := x.(map[string]any)
			if m["before"] != m["after"] {
				return fmt.Errorf("the frame load touched the audio: %v", w.out["audioLog"])
			}
		}
		if w.out["paused"] != false {
			return fmt.Errorf("the audio is not playing after the frame loads")
		}
		return nil
	})
	sc.Step(`^the address bar reads "([^"]*)" without embed=1$`, func(want string) error {
		if err := run(); err != nil {
			return err
		}
		h := cmStrs(w.out["history"])
		if len(h) == 0 || h[len(h)-1] != want {
			return fmt.Errorf("address bar %v, want %q", h, want)
		}
		return nil
	})
	sc.Step(`^the address bar reads "([^"]*)"$`, func(want string) error {
		if err := run(); err != nil {
			return err
		}
		h := cmStrs(w.out["history"])
		if len(h) == 0 || h[len(h)-1] != want {
			return fmt.Errorf("address bar %v, want %q", h, want)
		}
		return nil
	})
	sc.Step(`^the tab title is the frame page's title$`, func() error {
		if err := run(); err != nil {
			return err
		}
		if w.out["title"] != "Title of /photos" {
			return fmt.Errorf("tab title %q", w.out["title"])
		}
		return nil
	})
	sc.Step(`^sound is unlocked in the tap before the album is asked for$`, func() error {
		if err := run(); err != nil {
			return err
		}
		ev := cmStrs(w.out["syncEvents"])
		if len(ev) < 3 || ev[0] != "src:silent" || ev[1] != "play" || !strings.HasPrefix(ev[2], "fetch:/driver/roadtracks/album/42") {
			return fmt.Errorf("the tap did %v", ev)
		}
		return nil
	})
	sc.Step(`^the shell's audio element is playing$`, func() error {
		if err := run(); err != nil {
			return err
		}
		if w.out["paused"] != false {
			return fmt.Errorf("not playing: %v", w.out["audioLog"])
		}
		return nil
	})
	sc.Step(`^the audio is stopped and the card says "([^"]*)"$`, func(say string) error {
		if err := run(); err != nil {
			return err
		}
		if w.out["paused"] != true || w.out["status"] != say {
			return fmt.Errorf("paused=%v status=%q", w.out["paused"], w.out["status"])
		}
		return nil
	})
	sc.Step(`^the audio is playing track (\d+)$`, func(n int) error {
		if err := run(); err != nil {
			return err
		}
		if w.out["paused"] != false || !strings.HasPrefix(fmt.Sprint(w.out["trackText"]), fmt.Sprintf("Track %d of", n)) {
			return fmt.Errorf("paused=%v track=%q", w.out["paused"], w.out["trackText"])
		}
		return nil
	})
	sc.Step(`^the audio is stopped and the place is saved$`, func() error {
		if err := run(); err != nil {
			return err
		}
		if w.out["paused"] != true || num("statePosts") < 1 {
			return fmt.Errorf("paused=%v statePosts=%d", w.out["paused"], num("statePosts"))
		}
		return nil
	})
	sc.Step(`^the songs downloaded were tracks (\d+), (\d+) and (\d+) in that order$`, func(a, b, c int) error {
		if err := run(); err != nil {
			return err
		}
		got := map[int]bool{}
		var order []int
		for _, s := range cmStrs(w.out["trackFetches"]) {
			i, _ := strconv.Atoi(s)
			if !got[i] {
				got[i] = true
				order = append(order, i)
			}
		}
		if want := []int{a - 1, b - 1, c - 1}; fmt.Sprint(order) != fmt.Sprint(want) {
			return fmt.Errorf("downloaded %v, want %v", order, want)
		}
		return nil
	})
	sc.Step(`^the songs downloaded were tracks (\d+), (\d+), (\d+) and (\d+) in that order$`, func(a, b, c, d int) error {
		if err := run(); err != nil {
			return err
		}
		got := map[int]bool{}
		var order []int
		for _, s := range cmStrs(w.out["trackFetches"]) {
			i, _ := strconv.Atoi(s)
			if !got[i] {
				got[i] = true
				order = append(order, i)
			}
		}
		want := []int{a - 1, b - 1, c - 1, d - 1}
		if fmt.Sprint(order) != fmt.Sprint(want) {
			return fmt.Errorf("downloaded %v, want %v", order, want)
		}
		return nil
	})
	sc.Step(`^no two downloads ran at once$`, func() error {
		if err := run(); err != nil {
			return err
		}
		if num("maxInflight") != 1 {
			return fmt.Errorf("%d downloads ran at once", num("maxInflight"))
		}
		return nil
	})
	sc.Step(`^the card says (\d+) ready$`, func(n int) error {
		if err := run(); err != nil {
			return err
		}
		if w.out["nest"] != fmt.Sprintf("%d ready", n) {
			return fmt.Errorf("the card says %q", w.out["nest"])
		}
		return nil
	})
	sc.Step(`^the audio plays track (\d+) from its downloaded copy$`, func(n int) error {
		if err := run(); err != nil {
			return err
		}
		if !strings.HasPrefix(fmt.Sprint(w.out["src"]), "blob:") || !strings.HasPrefix(fmt.Sprint(w.out["trackText"]), fmt.Sprintf("Track %d of", n)) {
			return fmt.Errorf("src %v, %v", w.out["src"], w.out["trackText"])
		}
		return nil
	})
	sc.Step(`^playing it asked the network for nothing$`, func() error {
		if err := run(); err != nil {
			return err
		}
		n := 0
		for _, s := range cmStrs(w.out["trackFetches"]) {
			if s == "1" {
				n++
			}
		}
		if n != 1 {
			return fmt.Errorf("track 2 was fetched %d times", n)
		}
		return nil
	})
	sc.Step(`^the downloaded copy keeps the content type "([^"]*)"$`, func(ct string) error {
		if err := run(); err != nil {
			return err
		}
		for _, c := range w.out["created"].([]any) {
			if c.(map[string]any)["type"] != ct {
				return fmt.Errorf("a copy has type %v", c)
			}
		}
		if len(w.out["created"].([]any)) == 0 {
			return fmt.Errorf("nothing was downloaded")
		}
		return nil
	})
	sc.Step(`^the audio plays track (\d+) from its ordinary address$`, func(n int) error {
		if err := run(); err != nil {
			return err
		}
		if w.out["src"] != srcIs("t", n-1) && w.out["src"] != strings.Replace(srcIs("t", n-1), "sig=1", "sig=2", 1) {
			return fmt.Errorf("src %v", w.out["src"])
		}
		return nil
	})
	sc.Step(`^nothing was downloaded$`, func() error {
		if err := run(); err != nil {
			return err
		}
		if len(w.out["created"].([]any)) != 0 {
			return fmt.Errorf("something was downloaded")
		}
		return nil
	})
	sc.Step(`^exactly one log line says nesting is cold$`, func() error {
		if err := run(); err != nil {
			return err
		}
		n := 0
		for _, l := range cmStrs(w.out["logs"]) {
			if strings.Contains(l, "nest") && strings.Contains(l, "cold") {
				n++
			}
		}
		if n != 1 || len(w.out["logs"].([]any)) != 1 {
			return fmt.Errorf("log lines %v", w.out["logs"])
		}
		return nil
	})
	sc.Step(`^the card never shows a playback error$`, func() error {
		if err := run(); err != nil {
			return err
		}
		for _, s := range cmStrs(w.out["statusHistory"]) {
			if regexp.MustCompile(`(?i)could not|reconnect|unavailable|error`).MatchString(s) {
				return fmt.Errorf("the card said %q", s)
			}
		}
		return nil
	})
	sc.Step(`^the first track ending still plays track (\d+) from its ordinary address$`, func(n int) error {
		act(map[string]any{"do": "ended"})
		if err := run(); err != nil {
			return err
		}
		ord := fmt.Sprint(w.out["src"])
		if w.out["paused"] != false || (ord != srcIs("t", n-1) && ord != strings.Replace(srcIs("t", n-1), "sig=1", "sig=2", 1)) {
			return fmt.Errorf("src %v paused %v", w.out["src"], w.out["paused"])
		}
		return nil
	})
	sc.Step(`^the album was asked for again once$`, func() error {
		if err := run(); err != nil {
			return err
		}
		if num("albumFetches") != 2 {
			return fmt.Errorf("the album was asked for %d times", num("albumFetches"))
		}
		return nil
	})
	sc.Step(`^at most (\d+) songs are held$`, func(n int) error {
		if err := run(); err != nil {
			return err
		}
		if h := len(w.out["held"].([]any)); h > n || h == 0 {
			return fmt.Errorf("%d songs held", h)
		}
		return nil
	})
	sc.Step(`^the songs let go were the farthest from the playing track, each released$`, func() error {
		if err := run(); err != nil {
			return err
		}
		rev := w.out["revoked"].([]any)
		if len(rev) == 0 {
			return fmt.Errorf("nothing was let go")
		}
		songOf := map[int]int{}
		_ = songOf
		abs := func(x float64) float64 {
			if x < 0 {
				return -x
			}
			return x
		}
		for _, r := range rev {
			m := r.(map[string]any)
			idx := m["idx"].(float64)
			cur := m["cur"].(float64)
			for _, h := range m["held"].([]any) {
				if abs(h.(float64)-cur) > abs(idx-cur) {
					return fmt.Errorf("let go song %v (playing %v) while holding the farther %v", idx, cur, h)
				}
			}
		}
		return nil
	})
	sc.Step(`^the playing track is never let go$`, func() error {
		if err := run(); err != nil {
			return err
		}
		for _, r := range w.out["revoked"].([]any) {
			m := r.(map[string]any)
			if m["idx"] == m["cur"] {
				return fmt.Errorf("the playing track was let go: %v", m)
			}
		}
		return nil
	})
	sc.Step(`^every held song of the old album was released$`, func() error {
		if err := run(); err != nil {
			return err
		}
		created := w.out["created"].([]any)
		old := 0
		for i, c := range created {
			if c.(map[string]any)["album"] != "t" {
				continue
			}
			old++
			ok := false
			for _, r := range w.out["revoked"].([]any) {
				if int(r.(map[string]any)["created"].(float64)) == i {
					ok = true
				}
			}
			if !ok {
				return fmt.Errorf("a song of the old album was never released")
			}
		}
		if old == 0 {
			return fmt.Errorf("the old album held nothing")
		}
		return nil
	})
	sc.Step(`^the player never writes a song to disk, a database or a cache and offers no save$`, func() error {
		src := readFile(t, "static/js/player.js")
		if m := regexp.MustCompile(`indexedDB|caches\.|localStorage|sessionStorage|showSaveFilePicker|\.download\b|FileSystem|StorageManager|navigator\.storage`).FindString(src); m != "" {
			return fmt.Errorf("player.js uses %s", m)
		}
		return nil
	})

	// --- a tap never sticks (features/play_tap_never_sticks.feature) ---------------
	short := map[string]int{"stall": 30, "retry": 80, "giveUp": 80, "album": 150}
	sc.Step(`^the shell is idle with a resume point for album (\d+) at track (\d+)$`, func(id, n int) error {
		reset()
		w.resume = map[string]any{"album-id": id, "album": "First", "artist": "Artist", "idx": n - 1, "pos": 0}
		return nil
	})
	sc.Step(`^the listener presses Play$`, func() error { act(map[string]any{"do": "play"}); return nil })
	sc.Step(`^the Resume button is hidden$`, func() error {
		if err := run(); err != nil {
			return err
		}
		if w.out["resumeHidden"] != true {
			return fmt.Errorf("Resume hidden=%v", w.out["resumeHidden"])
		}
		return nil
	})
	sc.Step(`^the player card says "([^"]*)"$`, func(say string) error {
		if err := run(); err != nil {
			return err
		}
		if w.out["status"] != say {
			return fmt.Errorf("the card says %q (said %v)", w.out["status"], w.out["statusHistory"])
		}
		return nil
	})
	said := func(say string) (bool, error) {
		if err := run(); err != nil {
			return false, err
		}
		for _, s := range cmStrs(w.out["statusHistory"]) {
			if s == say {
				return true, nil
			}
		}
		return false, nil
	}
	sc.Step(`^the player card said "([^"]*)"$`, func(say string) error {
		ok, err := said(say)
		if err == nil && !ok {
			err = fmt.Errorf("the card never said %q: %v", say, w.out["statusHistory"])
		}
		return err
	})
	sc.Step(`^the player card never said "([^"]*)"$`, func(say string) error {
		ok, err := said(say)
		if err == nil && ok {
			err = fmt.Errorf("the card said %q", say)
		}
		return err
	})
	sc.Step(`^the music never starts playing$`, func() error { reset(); w.audioMode = "silent"; w.times = short; return nil })
	sc.Step(`^the music library never answers$`, func() error { reset(); w.albumMode = "hang"; w.times = short; return nil })
	sc.Step(`^the watchdog has had time to give up$`, func() error { act(map[string]any{"do": "wait", "ms": 600}); return nil })
	sc.Step(`^the Play button reads "([^"]*)"$`, func(want string) error {
		if err := run(); err != nil {
			return err
		}
		if w.out["playText"] != want {
			return fmt.Errorf("the Play button reads %q", w.out["playText"])
		}
		return nil
	})
	reported := func(event string) []map[string]any {
		var o []map[string]any
		for _, r := range w.out["reports"].([]any) {
			if m := r.(map[string]any); m["event"] == event {
				o = append(o, m)
			}
		}
		return o
	}
	sc.Step(`^the player reported "([^"]*)" with code "([^"]*)"$`, func(event, code string) error {
		if err := run(); err != nil {
			return err
		}
		for _, m := range reported(event) {
			if m["code"] == code {
				return nil
			}
		}
		return fmt.Errorf("no %s with code %s in %v", event, code, w.out["reports"])
	})
	sc.Step(`^the player reported "([^"]*)" with a time in milliseconds$`, func(event string) error {
		if err := run(); err != nil {
			return err
		}
		for _, m := range reported(event) {
			if regexp.MustCompile(`^\d+$`).MatchString(fmt.Sprint(m["ms"])) {
				return nil
			}
		}
		return fmt.Errorf("no %s with ms in %v", event, w.out["reports"])
	})
	sc.Step(`^every report is a form post kept alive past the page, carrying only event, ms, code, album_id and track_idx$`, func() error {
		if err := run(); err != nil {
			return err
		}
		allowed := map[string]bool{"event": true, "ms": true, "code": true, "album_id": true, "track_idx": true, "_keepalive": true, "_type": true}
		rs := w.out["reports"].([]any)
		if len(rs) == 0 {
			return fmt.Errorf("nothing was reported")
		}
		for _, r := range rs {
			m := r.(map[string]any)
			if m["_keepalive"] != true || m["_type"] != "application/x-www-form-urlencoded" {
				return fmt.Errorf("report %v is not a kept-alive form post", m)
			}
			for k := range m {
				if !allowed[k] {
					return fmt.Errorf("report carries %q: %v", k, m)
				}
			}
		}
		return nil
	})
	sc.Step(`^the stream stalls$`, func() error { act(map[string]any{"do": "audio", "event": "waiting"}); return nil })
	sc.Step(`^the stream plays again$`, func() error { act(map[string]any{"do": "audio", "event": "playing"}); return nil })
	sc.Step(`^the playing track was never downloaded$`, func() error {
		if err := run(); err != nil {
			return err
		}
		for _, s := range cmStrs(w.out["trackFetches"]) {
			if s == fmt.Sprint(num("cur")) {
				return fmt.Errorf("the playing track %d was downloaded: %v", num("cur"), w.out["trackFetches"])
			}
		}
		return nil
	})
	sc.Step(`^the first download began after the first sound$`, func() error {
		if err := run(); err != nil {
			return err
		}
		ev := cmStrs(w.out["events"])
		sound, dl := -1, -1
		for i, e := range ev {
			if e == "sound" && sound < 0 {
				sound = i
			}
			if strings.HasPrefix(e, "dl:") && dl < 0 {
				dl = i
			}
		}
		if sound < 0 || dl < 0 || dl < sound {
			return fmt.Errorf("events %v", ev)
		}
		return nil
	})
	sc.Step(`^the album was asked for once only$`, func() error {
		if err := run(); err != nil {
			return err
		}
		if num("albumFetches") != 1 {
			return fmt.Errorf("the album was asked for %d times", num("albumFetches"))
		}
		return nil
	})
	var dt map[string]any
	sc.Step(`^the driver asks Chester and Chester never answers$`, func() error {
		var err error
		dt, err = runNode(t, "static/js/drive_tracks_harness.mjs", map[string]any{"hang": true, "askMs": 50})
		return err
	})
	sc.Step(`^the card is told "([^"]*)"$`, func(want string) error {
		s := cmStrs(dt["said"])
		if len(s) != 1 || s[0] != want {
			return fmt.Errorf("the card was told %v", s)
		}
		return nil
	})
	sc.Step(`^the ask is over so the button can be pressed again$`, func() error {
		if dt["settled"] != true {
			return fmt.Errorf("the ask never settled")
		}
		return nil
	})
	sc.Step(`^the player reported the ask timed out$`, func() error {
		for _, r := range dt["reports"].([]any) {
			if r.(map[string]any)["event"] == "ask_timeout" {
				return nil
			}
		}
		return fmt.Errorf("reports %v", dt["reports"])
	})
	var fr map[string]any
	var topPage string
	sc.Step(`^the frame script runs in the top window on "([^"]*)"$`, func(p string) error {
		var err error
		topPage = p
		fr, err = runNode(t, "static/js/frame_harness.mjs", map[string]any{"kind": "load", "top": true, "page": p})
		return err
	})
	sc.Step(`^the frame script runs in the top window on the sign-in page "([^"]*)"$`, func(p string) error {
		var err error
		fr, err = runNode(t, "static/js/frame_harness.mjs", map[string]any{"kind": "load", "top": true, "page": p, "login": true})
		return err
	})
	sc.Step(`^the page is loaded again at "([^"]*)"$`, func(want string) error {
		if r := cmStrs(fr["replaced"]); len(r) != 1 || r[0] != want {
			return fmt.Errorf("loaded again at %v", r)
		}
		return nil
	})
	sc.Step(`^the page is not loaded again$`, func() error {
		if r := fr["replaced"].([]any); len(r) != 0 {
			return fmt.Errorf("loaded again at %v", r)
		}
		return nil
	})
	sc.Step(`^the frame script reported "([^"]*)"$`, func(event string) error {
		for _, r := range fr["reports"].([]any) {
			m := r.(map[string]any)
			if m["url"] == "/driver/roadtracks/event" && m["body"] == "event="+event && m["keepalive"] == true {
				return nil
			}
		}
		return fmt.Errorf("reports %v", fr["reports"])
	})
	sc.Step(`^a link to "([^"]*)" in the top window is left as "([^"]*)"$`, func(h, want string) error {
		o, err := runNode(t, "static/js/frame_harness.mjs", map[string]any{"kind": "link", "href": h, "top": true, "page": topPage})
		if err != nil {
			return err
		}
		if o["href"] != want {
			return fmt.Errorf("href %v", o["href"])
		}
		return nil
	})

	// --- the frame script ---------------------------------------------------------
	sc.Step(`^the frame script runs on a page of this site$`, func() error { return nil })
	link := func(href, target string) (map[string]any, error) {
		return runNode(t, "static/js/frame_harness.mjs", map[string]any{"kind": "link", "href": href, "target": target})
	}
	sc.Step(`^a link to "([^"]*)" opens a new tab$`, func(h string) error {
		o, err := link(h, "")
		if err != nil {
			return err
		}
		if o["target"] != "_blank" || !strings.Contains(fmt.Sprint(o["rel"]), "noopener") || o["href"] != h {
			return fmt.Errorf("%v", o)
		}
		return nil
	})
	sc.Step(`^a link to "([^"]*)" stays in the frame and carries embed=1$`, func(h string) error {
		o, err := link(h, "")
		if err != nil {
			return err
		}
		want := h + "?embed=1"
		if strings.Contains(h, "?") {
			want = strings.Replace(h, "#top", "&embed=1#top", 1)
		}
		if o["href"] != want || o["target"] != "" {
			return fmt.Errorf("href %v target %v, want %s", o["href"], o["target"], want)
		}
		return nil
	})
	sc.Step(`^a link to "([^"]*)" is left alone$`, func(h string) error {
		o, err := link(h, "")
		if err != nil {
			return err
		}
		if o["href"] != h || o["target"] != "" {
			return fmt.Errorf("%v", o)
		}
		return nil
	})
	sc.Step(`^a link to "([^"]*)" that opens a new tab is left alone$`, func(h string) error {
		o, err := link(h, "_blank")
		if err != nil {
			return err
		}
		if o["href"] != h || o["target"] != "_blank" {
			return fmt.Errorf("%v", o)
		}
		return nil
	})
	sc.Step(`^a link to "([^"]*)" opens in the top window$`, func(h string) error {
		o, err := link(h, "")
		if err != nil {
			return err
		}
		if o["target"] != "_top" {
			return fmt.Errorf("%v", o)
		}
		return nil
	})
	form := func(method, action, target string) (map[string]any, error) {
		return runNode(t, "static/js/frame_harness.mjs", map[string]any{"kind": "form", "method": method, "action": action, "target": target})
	}
	sc.Step(`^a post form to "([^"]*)" is sent with embed=1$`, func(a string) error {
		o, err := form("post", a, "")
		if err != nil {
			return err
		}
		if o["action"] != a+"?embed=1" {
			return fmt.Errorf("%v", o)
		}
		return nil
	})
	sc.Step(`^a search form to "([^"]*)" is sent with embed=1$`, func(a string) error {
		o, err := form("get", a, "")
		if err != nil {
			return err
		}
		if o["embedInput"] != true {
			return fmt.Errorf("%v", o)
		}
		return nil
	})
	sc.Step(`^the sign out form stops the music and is left to go to the top window$`, func() error {
		o, err := form("post", "/driver/logout", "_top")
		if err != nil {
			return err
		}
		if o["action"] != "/driver/logout" || o["target"] != "_top" || o["stopped"] != float64(1) {
			return fmt.Errorf("%v", o)
		}
		return nil
	})
}
