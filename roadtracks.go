package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RoadTracks: a search box on /driver that asks the MusicOrgan library for
// albums; a tap on a result plays that album inside RoadTrip (Kris, 2026-11-08).
// No audio passes through RoadTrip: the phone's audio element fetches straight
// from the address the library hands back. The shared read-only key is a Fly
// secret (ROADTRACKS_TOKEN): it is read once, sent only as a bearer header,
// and never logged, rendered or echoed.

const roadtracksTimeout = 20 * time.Second // a cold library can take a while to wake

type roadtracksSvc struct {
	base  string
	token string
	// linkBase, when set, replaces base at the front of every link handed to
	// the phone (ROADTRACKS_LINK_BASE). A KATE reaches its local MusicOrgan
	// over the container network, an address the phone cannot open.
	linkBase string
	client   *http.Client
}

// newRoadtracks returns nil, which is the feature off, for a blank token.
func newRoadtracks(token, base string) *roadtracksSvc {
	token, base = strings.TrimSpace(token), strings.TrimRight(strings.TrimSpace(base), "/")
	if token == "" {
		return nil
	}
	if base == "" {
		base = "https://music.opeongo.net"
	}
	return &roadtracksSvc{base: base, token: token, client: &http.Client{}}
}

// tracksView is the HUD's one word about the feature.
type tracksView struct {
	Enabled bool `json:"enabled"`
}

type trackAlbum struct {
	ID          int64  `json:"album_id"`
	Playable    bool   `json:"playable"`
	Album       string `json:"album"`
	Albumartist string `json:"albumartist"`
	Year        int    `json:"year,omitempty"`
	URL         string `json:"url"`
}

// search asks MusicOrgan. Every error is a word or two, never the URL or key.
func (s *roadtracksSvc) search(ctx context.Context, q string) ([]trackAlbum, error) {
	ctx, cancel := context.WithTimeout(ctx, roadtracksTimeout)
	defer cancel()
	u := s.base + "/api/roadtracks/search?limit=25&q=" + url.QueryEscape(q)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errors.New("error")
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("timeout")
		}
		return nil, errors.New("unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var out struct {
		Albums []struct {
			ID          int64  `json:"album_id"`
			Album       string `json:"album"`
			Albumartist string `json:"albumartist"`
			Year        int    `json:"year"`
			URL         string `json:"url"`
			Playable    *bool  `json:"playable"`
		} `json:"albums"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, errors.New("bad answer")
	}
	res := make([]trackAlbum, 0, len(out.Albums))
	for _, a := range out.Albums {
		link := a.URL
		// Only a link on MusicOrgan's own host is followed; anything else is
		// rebuilt from the id, so a hostile answer cannot plant a javascript: link.
		if !strings.HasPrefix(link, s.base+"/") {
			link = fmt.Sprintf("%s/album/%d/", s.base, a.ID)
		}
		if s.linkBase != "" {
			link = s.linkBase + strings.TrimPrefix(link, s.base)
		}
		// A library that does not say is an older one: list the album.
		playable := a.Playable == nil || *a.Playable
		res = append(res, trackAlbum{ID: a.ID, Playable: playable, Album: a.Album, Albumartist: a.Albumartist, Year: a.Year, URL: link})
	}
	return res, nil
}

func (a *App) roadtracksSearch(w http.ResponseWriter, r *http.Request) {
	if a.tracks == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/json")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Type something to search."})
		return
	}
	t0 := time.Now()
	albums, err := a.tracks.search(r.Context(), q)
	ms := time.Since(t0).Milliseconds()
	if err != nil {
		a.melt.Error("roadtracks_search", map[string]any{"qlen": len(q), "count": 0, "status": http.StatusBadGateway, "ms": ms, "reason": tracksReason(err)})
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Music library unavailable"})
		return
	}
	a.melt.Event("roadtracks_search", map[string]any{"qlen": len(q), "count": len(albums), "status": http.StatusOK, "ms": ms})
	_ = json.NewEncoder(w).Encode(map[string]any{"albums": albums})
}

// albumTrack and albumDoc are what the phone's player is handed. Src is a
// pointer so an unavailable or refused address is a JSON null, never "".
type albumTrack struct {
	ID        int64   `json:"id"`
	Track     int     `json:"track"`
	Disc      int     `json:"disc"`
	Title     string  `json:"title"`
	Artist    string  `json:"artist"`
	Length    float64 `json:"length"`
	Available bool    `json:"available"`
	Src       *string `json:"src"`
}

type albumDoc struct {
	ID          int64        `json:"album_id"`
	Album       string       `json:"album"`
	Albumartist string       `json:"albumartist"`
	Year        int          `json:"year,omitempty"`
	Art         string       `json:"art"`
	Playable    bool         `json:"playable"`
	Tracks      []albumTrack `json:"tracks"`
}

// phoneAddr turns an address the library gave into one the phone may use. A
// path-only address is put on the phone's base; an absolute https address with
// a host (a presigned storage URL) passes untouched; anything else, a
// javascript: or plain http one included, is refused with "".
func (s *roadtracksSvc) phoneAddr(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") && !strings.Contains(raw, `\`) {
		b := s.base
		if s.linkBase != "" {
			b = s.linkBase
		}
		return b + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	return raw
}

// album asks MusicOrgan for one album's tracks and rewrites every address.
// Every error is one word, never the URL or the key.
func (s *roadtracksSvc) album(ctx context.Context, id int64) (*albumDoc, error) {
	ctx, cancel := context.WithTimeout(ctx, roadtracksTimeout)
	defer cancel()
	u := fmt.Sprintf("%s/api/roadtracks/album/%d?cat=main", s.base, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errors.New("error")
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("timeout")
		}
		return nil, errors.New("unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var in struct {
		ID          int64  `json:"album_id"`
		Album       string `json:"album"`
		Albumartist string `json:"albumartist"`
		Year        int    `json:"year"`
		Art         string `json:"art"`
		Playable    *int   `json:"playable"`
		Tracks      []struct {
			ID        int64   `json:"id"`
			Track     int     `json:"track"`
			Disc      int     `json:"disc"`
			Title     string  `json:"title"`
			Artist    string  `json:"artist"`
			Length    float64 `json:"length"`
			Available bool    `json:"available"`
			Src       *string `json:"src"`
		} `json:"tracks"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&in); err != nil {
		return nil, errors.New("bad answer")
	}
	out := &albumDoc{ID: id, Album: in.Album, Albumartist: in.Albumartist, Year: in.Year,
		Art: s.phoneAddr(in.Art), Playable: in.Playable == nil || *in.Playable > 0, Tracks: make([]albumTrack, 0, len(in.Tracks))}
	for _, t := range in.Tracks {
		at := albumTrack{ID: t.ID, Track: t.Track, Disc: t.Disc, Title: t.Title, Artist: t.Artist, Length: t.Length, Available: t.Available}
		if t.Available && t.Src != nil {
			if a := s.phoneAddr(*t.Src); a != "" {
				at.Src = &a
			}
		}
		out.Tracks = append(out.Tracks, at)
	}
	return out, nil
}

// roadtracksAlbum: GET /driver/roadtracks/album/{id}. The audio is never
// fetched here; the phone's audio element goes straight to each src.
func (a *App) roadtracksAlbum(w http.ResponseWriter, r *http.Request) {
	if a.tracks == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/json")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unavailable"})
		return
	}
	t0 := time.Now()
	doc, err := a.tracks.album(r.Context(), id)
	ms := time.Since(t0).Milliseconds()
	if err != nil {
		a.melt.Error("roadtracks_album", map[string]any{"status": http.StatusBadGateway, "ms": ms, "reason": tracksReason(err)})
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unavailable"})
		return
	}
	a.melt.Event("roadtracks_album", map[string]any{"tracks": len(doc.Tracks), "status": http.StatusOK, "ms": ms})
	_ = json.NewEncoder(w).Encode(doc)
}

// tracksReason is the library error as one word for a MELT record: timeout,
// unreachable, status_503, bad_answer. The errors carry no URL or key.
func tracksReason(err error) string {
	return strings.ReplaceAll(strings.TrimSpace(err.Error()), " ", "_")
}

// resumeView is the server-held resume point, one per signed-in user.
type resumeView struct {
	AlbumID   int64   `json:"album_id"`
	Album     string  `json:"album"`
	Artist    string  `json:"artist"`
	TrackIdx  int     `json:"track_idx"`
	PositionS float64 `json:"position_s"`
	AgeS      int     `json:"age_s"`
}

const resumeMaxAge = 12 * time.Hour

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// roadtracksState: POST /driver/roadtracks/state writes the caller's one row.
// Numbers are validated; text is capped. Nothing is logged.
func (a *App) roadtracksState(w http.ResponseWriter, r *http.Request) {
	if a.tracks == nil {
		http.NotFound(w, r)
		return
	}
	id, e1 := strconv.ParseInt(r.PostFormValue("album_id"), 10, 64)
	idx, e2 := strconv.Atoi(r.PostFormValue("track_idx"))
	pos, e3 := strconv.ParseFloat(r.PostFormValue("position_s"), 64)
	if e1 != nil || e2 != nil || e3 != nil || id <= 0 || idx < 0 || idx > 999 || math.IsNaN(pos) || math.IsInf(pos, 0) || pos < 0 || pos > 86400 {
		http.Error(w, "bad resume point", http.StatusBadRequest)
		return
	}
	if err := a.store.SavePlayerState(a.userOf(r), id, clip(r.PostFormValue("album"), 200), clip(r.PostFormValue("artist"), 200), idx, pos, time.Now()); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	// A track that has just started sounding is also a play (plays.go). The
	// resume point is saved either way; a play that cannot be kept is not an error.
	if sp, ok := startedOf(r, id, idx); ok {
		_ = a.recordPlay(a.userOf(r), sp, time.Now())
	}
	w.WriteHeader(http.StatusNoContent)
}

// resumeFor is the caller's resume point, or nil when there is none or it is
// older than twelve hours. A read failure is no resume point, never a page failure.
func (a *App) resumeFor(r *http.Request) *resumeView {
	if a.tracks == nil {
		return nil
	}
	rv, err := a.store.PlayerState(a.userOf(r), time.Now())
	if err != nil {
		return nil
	}
	return rv
}
