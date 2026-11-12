package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/drive_tracks_plays_chesters_pick.feature. The Messages API
// is the apiStub and the library the moStub: nothing here reaches Anthropic or
// a real MusicOrgan. The tap is the real POST /chester/ask; its answer is then
// handed to the page's own driveTracks() (static/js/hud.js) under node, with a
// stand-in for the card player, so what the card is told is what the real
// script would do with what the real route sent. Audio on a real phone is not
// proven here.

type dtAlbum struct {
	id       int
	album    string
	artist   string
	playable bool
}

type dtWorld struct {
	t        *testing.T
	albums   []dtAlbum
	script   []http.HandlerFunc
	tracksOn bool
	moDown   bool
	spent    bool
	draws    []uint64
	seed     []func()
	mo       *moStub
	stub     *apiStub
	app      *App
	kris     *http.Cookie
	page     string
	code     int
	raw      string
	out      struct {
		Asked   []string            `json:"asked"`
		Said    []string            `json:"said"`
		Started []map[string]any    `json:"started"`
		Posted  []map[string]string `json:"posted"`
	}
}

var (
	dtButtonRe = regexp.MustCompile(`<button [^>]*id="hud-drivetracks"[^>]*>`)
	dtAnchorRe = regexp.MustCompile(`<a [^>]*id="hud-drivetracks"[^>]*>`)
	dtLeaveRe  = regexp.MustCompile(`window\.open\s*\(|location\.(href|assign|replace)\s*[=(]`)
)

func initDriveTracksScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &dtWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = dtWorld{t: t, tracksOn: true}
		return ctx, nil
	})
	sc.Step(`^the music library holds "([^"]*)" by "([^"]*)" as album (\d+), (playable|not playable)$`, w.holds)
	sc.Step(`^the music library also holds "([^"]*)" by "([^"]*)" as album (\d+), (playable|not playable)$`, w.holds)
	sc.Step(`^the trip-keeper is signed in with Chester and RoadTracks on$`, func() error { return nil })
	sc.Step(`^RoadTracks is switched off$`, func() error { w.tracksOn = false; return nil })
	sc.Step(`^the library for Drive Tracks is down$`, func() error { w.moDown = true; return nil })
	sc.Step(`^today's Chester limit is already spent$`, func() error { w.spent = true; return nil })
	sc.Step(`^the model will search the library for "([^"]*)"$`, w.willSearch)
	sc.Step(`^the model will then show the album "([^"]*)" because "([^"]*)"$`, func(ref, why string) error { return w.willShow(why, ref) })
	sc.Step(`^the model will then show the albums "([^"]*)" and "([^"]*)" because "([^"]*)"$`, func(a, b, why string) error { return w.willShow(why, a, b) })
	sc.Step(`^the model will (?:then )?write "([^"]*)"$`, w.willWrite)
	sc.Step(`^the traveller typed "([^"]*)" to Chester (\d+) (minutes|hours) ago$`, w.typed)
	sc.Step(`^Drive Tracks already picked "([^"]*)" by "([^"]*)" earlier today$`, w.pickedToday)
	sc.Step(`^the music library holds albums (\d+) to (\d+) by "([^"]*)", all playable$`, w.holdsRange)
	sc.Step(`^Drive Tracks already picked albums (\d+) to (\d+) on this trip$`, w.pickedRange)
	sc.Step(`^Drive Tracks picked album (\d+) "([^"]*)" by "([^"]*)" (\d+) days ago on (this|another) trip$`, w.pickedDaysAgo)
	sc.Step(`^the search answer Chester got says "([^"]*)"$`, w.searchAnswerSays)
	sc.Step(`^the search answer Chester got does not name "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" or "([^"]*)"$`, func(a, b, c, d, e, f string) error { return w.searchAnswerLacks(a, b, c, d, e, f) })
	sc.Step(`^the trouble record holds a drivetracks_pick for album (\d+) that avoided (\d+) albums and is not a repeat$`, w.meltPick)
	sc.Step(`^the trouble record holds a drivetracks_no_pick$`, w.meltNoPick)

	sc.Step(`^the search answer Chester got names no album by "([^"]*)"$`, w.searchAnswerNoArtist)
	sc.Step(`^Drive Tracks picked "([^"]*)" by "([^"]*)" long ago and then twelve other artists on this trip$`, w.pickedBeyondWindow)
	sc.Step(`^the draws for the taps are (\d+)(?: and (\d+))?$`, w.drawsAre)
	sc.Step(`^the model will pick the first album and write "([^"]*)"$`, w.willPickFirst)
	sc.Step(`^the driver taps Drive Tracks again$`, w.tap)
	sc.Step(`^the two questions carry different angle sentences$`, func() error { return w.anglesCompare(false) })
	sc.Step(`^the two questions carry the same angle sentence$`, func() error { return w.anglesCompare(true) })
	sc.Step(`^both questions say to use where the car is$`, w.bothUseCar)
	sc.Step(`^the trouble record holds a drivetracks_pick drawn with seed (\d+) and the angle "([^"]*)"$`, w.meltAngle)

	sc.Step(`^the driver opens the driver screen$`, w.openDriver)
	sc.Step(`^the driver taps Drive Tracks$`, w.tap)

	sc.Step(`^Drive Tracks is a button with no address and no new tab$`, w.plainButton)
	sc.Step(`^the same page holds the card player$`, w.cardOnPage)
	sc.Step(`^the driver screen's script opens no window and goes to no address$`, w.scriptStays)
	sc.Step(`^there is no Drive Tracks button$`, func() error {
		if dtButtonRe.MatchString(w.page) || dtAnchorRe.MatchString(w.page) {
			return fmt.Errorf("a Drive Tracks control with the library off")
		}
		return nil
	})
	sc.Step(`^the card is told to start album (\d+) "([^"]*)" by "([^"]*)"$`, w.started)
	sc.Step(`^the card is told to start no album$`, func() error {
		if len(w.out.Started) != 0 {
			return fmt.Errorf("the card was told to start %v", w.out.Started)
		}
		return nil
	})
	sc.Step(`^the card player's own album route hands over that album's first track to play$`, w.albumRoute)
	sc.Step(`^Chester was asked once, in quick mode, in three rounds$`, w.askedOnce)
	sc.Step(`^the question was logged with the preset "([^"]*)" and the album it picked$`, w.logged)
	sc.Step(`^exactly one album card came back, for album (\d+)$`, w.oneCard)
	sc.Step(`^the question sent holds "([^"]*)"$`, func(s string) error { return w.told(s, true) })
	sc.Step(`^the question sent does not hold "([^"]*)"$`, func(s string) error { return w.told(s, false) })
	sc.Step(`^Chester's search result held "([^"]*)" and not "([^"]*)"$`, w.searchHeld)
	sc.Step(`^the card shows the sentence "([^"]*)"$`, w.cardSays)
	sc.Step(`^the Chester page offers no Drive Tracks question$`, w.noPreset)
	sc.Step(`^nothing sent to the model holds the library key or address and the card's answer holds no key$`, w.noLeak)
}

func (w *dtWorld) holds(album, artist, id, play string) error {
	n, _ := strconv.Atoi(id)
	w.albums = append(w.albums, dtAlbum{n, album, artist, play == "playable"})
	return nil
}

// build makes the app once the scenario's givens are in: the library answers in
// the real MusicOrgan search shape (album_id, album, albumartist, year, tracks,
// url, playable as a bool), the Messages API is scripted, and the rows seeded.
func (w *dtWorld) build() error {
	if w.app != nil {
		return nil
	}
	w.mo = newMO(w.t)
	var rows []string
	for _, a := range w.albums {
		rows = append(rows, fmt.Sprintf(`{"album_id":%d,"album":%q,"albumartist":%q,"year":1969,"tracks":17,"url":"BASE/album/%d/","playable":%t}`,
			a.id, a.album, a.artist, a.id, a.playable))
	}
	w.mo.body = `{"q":"x","albums":[` + strings.Join(rows, ",") + `],"artists":[],"playlists":[]}`
	if w.moDown {
		w.mo.status, w.mo.body = 503, `{"error":"down `+rtKey+`"}`
	}
	w.stub = newAPIStub(w.t, w.script...)
	w.app = chesterApp(w.t, w.stub)
	if len(w.draws) > 0 {
		n := 0
		w.app.driveSeed = func() uint64 { d := w.draws[n%len(w.draws)]; n++; return d }
	}
	if w.tracksOn {
		w.app.tracks = newRoadtracks(rtKey, w.mo.srv.URL)
	}
	w.kris = asUser(w.t, w.app, "kris", roleAdmin)
	w.app.melt = newMeltSinks("", "test", filepath.Join(w.t.TempDir(), "melt", "roadtrip.jsonl"))
	if w.spent {
		if err := w.app.store.InsertChesterQuery(&ChesterQuery{User: "kris", AskedAt: utcString(time.Now()), TZ: "UTC",
			Mode: "quick", Question: "q", Model: stubModel, USD: 100}); err != nil {
			return err
		}
	}
	for _, f := range w.seed {
		f()
	}
	return nil
}

func (w *dtWorld) addRow(q ChesterQuery) {
	q.User, q.TZ, q.Mode, q.Model = "kris", "UTC", "quick", stubModel
	if err := w.app.store.InsertChesterQuery(&q); err != nil {
		w.t.Fatal(err)
	}
}

func (w *dtWorld) typed(q, n, unit string) error {
	d, _ := strconv.Atoi(n)
	age := time.Duration(d) * time.Minute
	if unit == "hours" {
		age = time.Duration(d) * time.Hour
	}
	w.seed = append(w.seed, func() { w.addRow(ChesterQuery{AskedAt: utcString(time.Now().Add(-age)), Question: q}) })
	return nil
}

func (w *dtWorld) pickedToday(album, artist string) error {
	w.seed = append(w.seed, func() {
		w.addRow(ChesterQuery{Ride: w.app.activeSlug(), AskedAt: utcString(time.Now().Add(-time.Second)), Preset: chesterDriveTracksPreset, Question: "earlier",
			Music: []chesterMusicCard{{AlbumID: 43, Album: album, Artist: artist, Label: "Play"}}})
	})
	return nil
}

func (w *dtWorld) willSearch(q string) error {
	w.script = append(w.script, stream(events(evStart(stubModel, 100, 0, 0), evThinking(0),
		evClientTool(1, "toolu_s"+strconv.Itoa(len(w.script)), "search_music", js(map[string]any{"query": q})), evEnd("tool_use", 20, 0, nil))...))
	return nil
}

func (w *dtWorld) willShow(why string, refs ...string) error {
	var as []any
	for _, r := range refs {
		as = append(as, map[string]any{"ref": r, "why": why})
	}
	w.script = append(w.script, stream(events(evStart(stubModel, 120, 0, 0), evThinking(0),
		evClientTool(1, "toolu_m"+strconv.Itoa(len(w.script)), "show_music", js(map[string]any{"albums": as})), evEnd("tool_use", 20, 0, nil))...))
	return nil
}

func (w *dtWorld) willWrite(text string) error {
	w.script = append(w.script, stream(events(evStart(stubModel, 150, 0, 0), evText(0, text), evEnd("end_turn", 10, 0, nil))...))
	return nil
}

func (w *dtWorld) openDriver() error {
	if err := w.build(); err != nil {
		return err
	}
	rec := doAs(w.t, w.app, w.kris, "GET", "/driver", nil)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("driver screen answered %d", rec.Code)
	}
	w.page = rec.Body.String()
	return nil
}

// tap is the real POST the button's script makes, then the page's own script
// run over the answer.
func (w *dtWorld) tap() error {
	if err := w.build(); err != nil {
		return err
	}
	rec := doAs(w.t, w.app, w.kris, "POST", "/chester/ask", url.Values{"source": {chesterSourceDrive}})
	w.code, w.raw = rec.Code, rec.Body.String()
	for _, chunked := range []bool{false, true} {
		in, _ := json.Marshal(map[string]any{"status": w.code, "body": w.raw, "chunked": chunked, "posted": "source=drivetracks"})
		cmd := exec.Command("node", "static/js/drive_tracks_harness.mjs")
		cmd.Stdin = strings.NewReader(string(in))
		b, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("node could not run the page's script (it is needed for this step): %v", err)
		}
		var got = w.out
		if err := json.Unmarshal(b, &got); err != nil {
			return fmt.Errorf("the harness answered %q", b)
		}
		if chunked && fmt.Sprint(got.Started, got.Said) != fmt.Sprint(w.out.Started, w.out.Said) {
			return fmt.Errorf("a split-up stream reads differently: %v %v against %v %v", got.Started, got.Said, w.out.Started, w.out.Said)
		}
		w.out = got
	}
	return nil
}

func (w *dtWorld) plainButton() error {
	tag := dtButtonRe.FindString(w.page)
	if tag == "" || dtAnchorRe.MatchString(w.page) {
		return fmt.Errorf("Drive Tracks is not a <button>: %q", tag)
	}
	for _, bad := range []string{"href", "target", "formaction", "formtarget"} {
		if strings.Contains(tag, bad) {
			return fmt.Errorf("the button carries %s: %s", bad, tag)
		}
	}
	if !strings.Contains(tag, `type="button"`) || !strings.Contains(w.page[strings.Index(w.page, tag):], "Ask Chester for an album</button>") {
		return fmt.Errorf("the button is not a labelled type=button: %s", tag)
	}
	return nil
}

func (w *dtWorld) cardOnPage() error {
	for _, id := range []string{`id="rt-player"`, `id="rt-audio"`} {
		if !strings.Contains(w.page, id) {
			return fmt.Errorf("the driver screen lacks %s", id)
		}
	}
	return nil
}

func (w *dtWorld) scriptStays() error {
	b, err := os.ReadFile("static/js/hud.js")
	if err != nil {
		return err
	}
	if m := dtLeaveRe.FindString(string(b)); m != "" {
		return fmt.Errorf("static/js/hud.js leaves the page: %s", m)
	}
	if !strings.Contains(string(b), "source: 'drivetracks'") || !strings.Contains(string(b), "player.start(") {
		return fmt.Errorf("static/js/hud.js does not ask for source=drivetracks and start the card player")
	}
	return nil
}

func (w *dtWorld) started(id int, album, artist string) error {
	if len(w.out.Started) != 1 {
		return fmt.Errorf("want the card told to start one album, got %v (said %v; answer %s)", w.out.Started, w.out.Said, w.raw)
	}
	s := w.out.Started[0]
	if int(s["album_id"].(float64)) != id || s["album"] != album || s["albumartist"] != artist {
		return fmt.Errorf("started %v", s)
	}
	if len(w.out.Asked) == 0 {
		return fmt.Errorf("the card was never told Chester was being asked")
	}
	return nil
}

func (w *dtWorld) albumRoute() error {
	id := int(w.out.Started[0]["album_id"].(float64))
	rec := doAs(w.t, w.app, w.kris, "GET", "/driver/roadtracks/album/"+strconv.Itoa(id), nil)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("the album route answered %d: %s", rec.Code, rec.Body.String())
	}
	var doc struct {
		Tracks []struct {
			Src *string `json:"src"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Tracks) == 0 || doc.Tracks[0].Src == nil || *doc.Tracks[0].Src == "" {
		return fmt.Errorf("no first track to play: %s", rec.Body.String())
	}
	return nil
}

func (w *dtWorld) askedOnce() error {
	rows, err := w.app.store.ChesterQueries(0)
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0].Mode != "quick" {
		return fmt.Errorf("want one quick question in the log, got %d", len(rows))
	}
	if n := len(w.stub.requests()); n != chesterRounds {
		return fmt.Errorf("the model was asked %d times, want %d (search, show, write)", n, chesterRounds)
	}
	return nil
}

func (w *dtWorld) logged(preset string) error {
	rows, _ := w.app.store.ChesterQueriesOf("kris")
	if len(rows) != 1 || rows[0].Preset != preset || len(rows[0].Music) != 1 || rows[0].Music[0].AlbumID != 42 {
		return fmt.Errorf("the log holds %+v", rows)
	}
	if _, ok := chesterPreset(preset); ok {
		return fmt.Errorf("%q is one of the preset buttons", preset)
	}
	return nil
}

func (w *dtWorld) oneCard(id int) error {
	n := 0
	for _, line := range strings.Split(w.raw, "\n") {
		var ev struct {
			T     string `json:"t"`
			Music []struct {
				AlbumID int `json:"album_id"`
			} `json:"music"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.T == "music" {
			for _, c := range ev.Music {
				n++
				if c.AlbumID != id {
					return fmt.Errorf("the card is for album %d, want %d", c.AlbumID, id)
				}
			}
		}
	}
	if n != 1 {
		return fmt.Errorf("want exactly one album card, got %d (%s)", n, w.raw)
	}
	return nil
}

// strs is every string value in a JSON document.
func strs(v any, out *[]string) {
	switch x := v.(type) {
	case string:
		*out = append(*out, x)
	case []any:
		for _, e := range x {
			strs(e, out)
		}
	case map[string]any:
		for _, e := range x {
			strs(e, out)
		}
	}
}

func (w *dtWorld) requestText(n int) string {
	var doc any
	_ = json.Unmarshal(w.stub.requests()[n].Body, &doc)
	var all []string
	strs(doc, &all)
	return strings.Join(all, "\n")
}

func (w *dtWorld) told(s string, want bool) error {
	if got := strings.Contains(w.requestText(0), s); got != want {
		return fmt.Errorf("Chester told %q = %v, want %v", s, got, want)
	}
	return nil
}

func (w *dtWorld) searchHeld(yes, no string) error {
	var all strings.Builder
	for i := 1; i < len(w.stub.requests()); i++ {
		all.WriteString(w.requestText(i))
	}
	if !strings.Contains(all.String(), yes) || strings.Contains(all.String(), no) {
		return fmt.Errorf("the search result sent back was %q", all.String())
	}
	return nil
}

func (w *dtWorld) cardSays(s string) error {
	for _, said := range w.out.Said {
		if strings.Contains(said, s) {
			return nil
		}
	}
	return fmt.Errorf("the card said %v, want %q", w.out.Said, s)
}

func (w *dtWorld) noPreset() error {
	for _, p := range chesterPresets {
		if p.ID == chesterDriveTracksPreset {
			return fmt.Errorf("Drive Tracks is a preset")
		}
	}
	rec := doAs(w.t, w.app, w.kris, "GET", "/chester", nil)
	if rec.Code != http.StatusOK || strings.Contains(strings.ToLower(rec.Body.String()), "drivetracks") || strings.Contains(rec.Body.String(), "Pick one album") {
		return fmt.Errorf("the Chester page offers Drive Tracks (status %d)", rec.Code)
	}
	return nil
}

func (w *dtWorld) noLeak() error {
	for i := range w.stub.requests() {
		b := string(w.stub.requests()[i].Body)
		for _, bad := range []string{rtKey, w.mo.srv.URL, "127.0.0.1", "/album/"} {
			if strings.Contains(b, bad) {
				return fmt.Errorf("request %d to the model holds %q", i, bad)
			}
		}
	}
	// The card's own href is the library's album page, as on the Chester page;
	// the key is the one thing that is never in an answer.
	for _, bad := range []string{rtKey} {
		if strings.Contains(w.raw, bad) {
			return fmt.Errorf("the answer to the card holds %q", bad)
		}
	}
	return nil
}

func (w *dtWorld) holdsRange(from, to int, artist string) error {
	for i := from; i <= to; i++ {
		w.albums = append(w.albums, dtAlbum{i, "Album " + strconv.Itoa(i), artist, true})
	}
	return nil
}

// pickedRange seeds one Drive Tracks row per album, album 1 the oldest, all on
// the active trip and spread over the last hours.
func (w *dtWorld) pickedRange(from, to int) error {
	w.seed = append(w.seed, func() {
		for i := from; i <= to; i++ {
			var a dtAlbum
			for _, x := range w.albums {
				if x.id == i {
					a = x
				}
			}
			w.addRow(ChesterQuery{Ride: w.app.activeSlug(), AskedAt: utcString(time.Now().Add(-time.Duration(to-i+1) * 20 * time.Minute)),
				Preset: chesterDriveTracksPreset, Question: "earlier",
				Music: []chesterMusicCard{{AlbumID: int64(a.id), Album: a.album, Artist: a.artist, Label: "Play"}}})
		}
	})
	return nil
}

func (w *dtWorld) pickedDaysAgo(id int, album, artist string, days int, which string) error {
	w.seed = append(w.seed, func() {
		ride := w.app.activeSlug()
		if which == "another" {
			ride = "some-other-trip-2026-09"
		}
		w.addRow(ChesterQuery{Ride: ride, AskedAt: utcString(time.Now().Add(-time.Duration(days) * 24 * time.Hour)),
			Preset: chesterDriveTracksPreset, Question: "earlier",
			Music: []chesterMusicCard{{AlbumID: int64(id), Album: album, Artist: artist, Label: "Play"}}})
	})
	return nil
}

// searchAnswerSays reads the tool result the server handed back for the first
// search_music call: the second request to the model carries it.
func (w *dtWorld) searchAnswerSays(s string) error {
	got, err := w.searchAnswer()
	if err != nil {
		return err
	}
	if !strings.Contains(got, s) {
		return fmt.Errorf("the search answer was %q", got)
	}
	return nil
}

func (w *dtWorld) searchAnswerLacks(names ...string) error {
	got, err := w.searchAnswer()
	if err != nil {
		return err
	}
	for _, n := range names {
		if strings.Contains(got, n) {
			return fmt.Errorf("the search answer offered %q: %q", n, got)
		}
	}
	return nil
}

func (w *dtWorld) searchAnswer() (string, error) {
	reqs := w.stub.requests()
	if len(reqs) < 2 {
		return "", fmt.Errorf("the model was asked %d times; no search answer went back", len(reqs))
	}
	var doc struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(reqs[1].Body, &doc); err != nil || len(doc.Messages) == 0 {
		return "", fmt.Errorf("the second request was not readable: %v", err)
	}
	var all []string
	var v any
	_ = json.Unmarshal(doc.Messages[len(doc.Messages)-1].Content, &v)
	strs(v, &all)
	return strings.Join(all, "\n"), nil
}

// meltRecords reads the signed-in trouble record route, waiting briefly for
// the background writer.
func (w *dtWorld) meltRecords(event string) ([]map[string]any, error) {
	for i := 0; i < 100; i++ {
		rec := doAs(w.t, w.app, w.kris, "GET", "/driver/melt.json?stream=all&event="+event, nil)
		if rec.Code != http.StatusOK {
			return nil, fmt.Errorf("/driver/melt.json answered %d", rec.Code)
		}
		var doc struct {
			Records []map[string]any `json:"records"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			return nil, err
		}
		if len(doc.Records) > 0 {
			return doc.Records, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, fmt.Errorf("no %s record was written", event)
}

func (w *dtWorld) meltPick(id, avoided int) error {
	recs, err := w.meltRecords("drivetracks_pick")
	if err != nil {
		return err
	}
	r := recs[0]
	if r["stream"] != "events" || r["album_id"] != float64(id) || r["avoided"] != float64(avoided) || r["repeat"] != false {
		return fmt.Errorf("the drivetracks_pick record is %v", r)
	}
	return nil
}

func (w *dtWorld) meltNoPick() error {
	recs, err := w.meltRecords("drivetracks_no_pick")
	if err != nil {
		return err
	}
	if recs[0]["stream"] != "errors" || recs[0]["reason"] == nil {
		return fmt.Errorf("the drivetracks_no_pick record is %v", recs[0])
	}
	return nil
}

func (w *dtWorld) searchAnswerNoArtist(artist string) error {
	got, err := w.searchAnswer()
	if err != nil {
		return err
	}
	if strings.Contains(got, " by "+artist) {
		return fmt.Errorf("the search answer offered an album by %q: %q", artist, got)
	}
	return nil
}

// pickedBeyondWindow seeds one old pick and then twelve newer ones by twelve
// other artists, all on the active trip.
func (w *dtWorld) pickedBeyondWindow(album, artist string) error {
	w.seed = append(w.seed, func() {
		ride := w.app.activeSlug()
		w.addRow(ChesterQuery{Ride: ride, AskedAt: utcString(time.Now().Add(-5 * 24 * time.Hour)), Preset: chesterDriveTracksPreset, Question: "earlier",
			Music: []chesterMusicCard{{AlbumID: 90, Album: album, Artist: artist, Label: "Play"}}})
		for i := 1; i <= driveArtistWindow; i++ {
			w.addRow(ChesterQuery{Ride: ride, AskedAt: utcString(time.Now().Add(-time.Duration(driveArtistWindow-i+1) * time.Hour)), Preset: chesterDriveTracksPreset, Question: "earlier",
				Music: []chesterMusicCard{{AlbumID: int64(200 + i), Album: "Record " + strconv.Itoa(i), Artist: "Band " + strconv.Itoa(i), Label: "Play"}}})
		}
	})
	return nil
}

func (w *dtWorld) drawsAre(a, b string) error {
	for _, s := range []string{a, b} {
		if s != "" {
			n, _ := strconv.ParseUint(s, 10, 64)
			w.draws = append(w.draws, n)
		}
	}
	return nil
}

// willPickFirst scripts one whole tap: search, show the first reference, write.
func (w *dtWorld) willPickFirst(text string) error {
	if err := w.willSearch("x"); err != nil {
		return err
	}
	if err := w.willShow("Familiar", "m1"); err != nil {
		return err
	}
	return w.willWrite(text)
}

// angleLine is the line of tap n's question that is the angle sentence: the
// first request of that tap is request number n*chesterRounds.
func (w *dtWorld) angleLine(tap int) (string, error) {
	reqs := w.stub.requests()
	if len(reqs) <= tap*chesterRounds {
		return "", fmt.Errorf("tap %d never reached the model (%d requests)", tap, len(reqs))
	}
	var doc any
	_ = json.Unmarshal(reqs[tap*chesterRounds].Body, &doc)
	var all []string
	strs(doc, &all)
	for _, s := range all {
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(line, "This time, ") {
				return line, nil
			}
		}
	}
	return "", fmt.Errorf("tap %d carried no angle sentence", tap)
}

func (w *dtWorld) anglesCompare(same bool) error {
	a, err := w.angleLine(0)
	if err != nil {
		return err
	}
	b, err := w.angleLine(1)
	if err != nil {
		return err
	}
	if (a == b) != same {
		return fmt.Errorf("the angles were %q and %q, want same=%v", a, b, same)
	}
	return nil
}

func (w *dtWorld) bothUseCar() error {
	for tap := 0; tap < 2; tap++ {
		var doc any
		reqs := w.stub.requests()
		if len(reqs) <= tap*chesterRounds {
			return fmt.Errorf("tap %d never reached the model", tap)
		}
		_ = json.Unmarshal(reqs[tap*chesterRounds].Body, &doc)
		var all []string
		strs(doc, &all)
		if !strings.Contains(strings.Join(all, "\n"), "Use where the car is") {
			return fmt.Errorf("tap %d does not say to use where the car is", tap)
		}
	}
	return nil
}

func (w *dtWorld) meltAngle(seed int, angle string) error {
	recs, err := w.meltRecords("drivetracks_pick")
	if err != nil {
		return err
	}
	r := recs[0]
	if r["seed"] != float64(seed) || r["angle_text"] != angle {
		return fmt.Errorf("the drivetracks_pick record is %v", r)
	}
	return nil
}
