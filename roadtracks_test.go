package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const rtKey = "rt-secret-token-123"

// moAlbum42 is the album answer: a path-only src, an https src, two hostile
// srcs, and a track that is not available (src null).
const moAlbum42 = `{"album_id":42,"album":"Abbey Road","albumartist":"The Beatles","year":1969,"art":"/album/42/art","playable":true,"tracks":[` +
	`{"id":1,"track":1,"disc":1,"title":"One","artist":"A","length":100,"available":true,"src":"/media/t1?sig=x"},` +
	`{"id":2,"track":2,"disc":1,"title":"Two","artist":"A","length":100,"available":true,"src":"https://cdn.example.test/t2.mp3?sig=y"},` +
	`{"id":3,"track":3,"disc":1,"title":"Three","artist":"A","length":100,"available":true,"src":"javascript:alert(1)"},` +
	`{"id":4,"track":4,"disc":1,"title":"Four","artist":"A","length":100,"available":true,"src":"http://elsewhere.example.test/x.mp3"},` +
	`{"id":5,"track":5,"disc":1,"title":"Five","artist":"A","length":100,"available":false,"src":null}]}`

type moStub struct {
	srv    *httptest.Server
	mu     sync.Mutex
	auth   string
	query  string
	status int
	body   string
}

func newMO(t *testing.T) *moStub {
	s := &moStub{status: 200, body: `{"q":"abbey","albums":[` +
		`{"album_id":42,"album":"Abbey Road","albumartist":"The Beatles","year":1969,"tracks":17,"url":"BASE/album/42/"},` +
		`{"album_id":7,"album":"Other","albumartist":"X","year":2001,"tracks":3,"url":"javascript:alert(1)"}],` +
		`"artists":[],"playlists":[]}`}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auth, s.query = r.Header.Get("Authorization"), r.URL.Query().Get("q")
		st, body := s.status, strings.ReplaceAll(s.body, "BASE", s.srv.URL)
		s.mu.Unlock()
		if r.URL.Path == "/api/roadtracks/album/42" {
			w.WriteHeader(st)
			w.Write([]byte(strings.ReplaceAll(moAlbum42, "BASE", s.srv.URL)))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/roadtracks/album/") {
			w.WriteHeader(404)
			w.Write([]byte(`{"error":"no such album ` + rtKey + `"}`))
			return
		}
		if r.URL.Path != "/api/roadtracks/search" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.WriteHeader(st)
		w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func tracksApp(t *testing.T, mo *moStub) (*App, *http.Cookie) {
	app := testApp(t)
	app.tracks = newRoadtracks(rtKey, mo.srv.URL)
	return app, asUser(t, app, "kris", roleAdmin)
}

type rtAnswer struct {
	Albums []struct {
		Album       string `json:"album"`
		Albumartist string `json:"albumartist"`
		Year        int    `json:"year"`
		URL         string `json:"url"`
	} `json:"albums"`
	Error string `json:"error"`
}

func TestRoadTracksSearchReturnsAlbumLinks(t *testing.T) {
	mo := newMO(t)
	app, c := tracksApp(t, mo)
	w := doAs(t, app, c, "GET", "/driver/roadtracks/search?q=abbey", nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var a rtAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil || len(a.Albums) != 2 {
		t.Fatalf("%v %s", err, w.Body.String())
	}
	if a.Albums[0].URL != mo.srv.URL+"/album/42/" || a.Albums[0].Album != "Abbey Road" || a.Albums[0].Year != 1969 {
		t.Fatalf("first: %+v", a.Albums[0])
	}
	if a.Albums[1].URL != mo.srv.URL+"/album/7/" {
		t.Fatalf("a foreign url must be rebuilt from the id: %q", a.Albums[1].URL)
	}
	if mo.auth != "Bearer "+rtKey || mo.query != "abbey" {
		t.Fatalf("stub saw auth=%q q=%q", mo.auth, mo.query)
	}
	if strings.Contains(w.Body.String(), rtKey) {
		t.Fatal("key in the answer")
	}
}

func TestRoadTracksSearchIsSignedInOnly(t *testing.T) {
	mo := newMO(t)
	app, _ := tracksApp(t, mo)
	w := doAs(t, app, nil, "GET", "/driver/roadtracks/search?q=abbey", nil)
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/driver/login") || mo.auth != "" {
		t.Fatalf("anonymous: %d %q (stub saw %q)", w.Code, w.Header().Get("Location"), mo.auth)
	}
}

func TestRoadTracksFailuresAreHonestAndNeverLeakTheKey(t *testing.T) {
	mo := newMO(t)
	app, c := tracksApp(t, mo)
	for _, st := range []int{401, 503, 500} {
		mo.status, mo.body = st, `{"error":"x `+rtKey+`"}`
		w := doAs(t, app, c, "GET", "/driver/roadtracks/search?q=abbey", nil)
		if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "Music library unavailable") {
			t.Errorf("stub %d: %d %s", st, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), rtKey) {
			t.Errorf("stub %d leaked the key", st)
		}
	}
	mo.status = 200
	if page := doAs(t, app, c, "GET", "/driver", nil).Body.String(); strings.Contains(page, rtKey) {
		t.Fatal("key on the driver page")
	}
	if w := doAs(t, app, c, "GET", "/driver/roadtracks/search?q=%20", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("blank q: %d", w.Code)
	}
}

func TestRoadTracksOffMeansNoCardAndNoRoute(t *testing.T) {
	app := testApp(t)
	c := asUser(t, app, "kris", roleAdmin)
	app.tracks = newRoadtracks("", "https://music.example")
	if app.tracks != nil {
		t.Fatal("blank token must switch the feature off")
	}
	if page := doAs(t, app, c, "GET", "/driver", nil).Body.String(); strings.Contains(page, "RoadTracks") {
		t.Fatal("card shown with the feature off")
	}
	if w := doAs(t, app, c, "GET", "/driver/roadtracks/search?q=a", nil); w.Code != http.StatusNotFound {
		t.Fatalf("route: %d", w.Code)
	}
}

func TestRoadTracksCardShownWhenOn(t *testing.T) {
	app, c := tracksApp(t, newMO(t))
	if page := doAs(t, app, c, "GET", "/driver", nil).Body.String(); !strings.Contains(page, "RoadTracks") {
		t.Fatal("no card")
	}
	if h := hudAt(t, app, c, ""); h.Tracks == nil || !h.Tracks.Enabled {
		t.Fatalf("hud tracks: %+v", h.Tracks)
	}
}

// A KATE calls its local MusicOrgan over the container network
// (ROADTRACKS_BASE=http://musicorgan:8000) but the phone can only open the LAN
// address, so ROADTRACKS_LINK_BASE rewrites the links it hands back. Kris,
// 2026-11-08: "local KATE roadtrip uses local KATE musicorgan".
func TestRoadTracksLinkBaseRewritesLinksForThePhone(t *testing.T) {
	mo := newMO(t)
	app, c := tracksApp(t, mo)
	app.tracks.linkBase = "http://192.0.2.10:8810"
	w := doAs(t, app, c, "GET", "/driver/roadtracks/search?q=abbey", nil)
	var a rtAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil || len(a.Albums) != 2 {
		t.Fatalf("%v %s", err, w.Body.String())
	}
	if a.Albums[0].URL != "http://192.0.2.10:8810/album/42/" || a.Albums[1].URL != "http://192.0.2.10:8810/album/7/" {
		t.Fatalf("links not on the link base: %q %q", a.Albums[0].URL, a.Albums[1].URL)
	}
	if strings.Contains(w.Body.String(), mo.srv.URL) {
		t.Fatal("the internal base leaked into the answer")
	}
}
