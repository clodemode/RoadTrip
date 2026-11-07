package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RoadTracks: a search box on /driver that asks the MusicOrgan library for
// albums and hands back links to its player. No audio passes through RoadTrip;
// a tap opens MusicOrgan in a new tab. The shared read-only key is a Fly
// secret (ROADTRACKS_TOKEN): it is read once, sent only as a bearer header,
// and never logged, rendered or echoed.

const roadtracksTimeout = 5 * time.Second

type roadtracksSvc struct {
	base   string
	token  string
	client *http.Client
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
		res = append(res, trackAlbum{Album: a.Album, Albumartist: a.Albumartist, Year: a.Year, URL: link})
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
	albums, err := a.tracks.search(r.Context(), q)
	if err != nil {
		a.melt.Event("roadtracks_search", map[string]any{"qlen": len(q), "count": 0, "status": http.StatusBadGateway})
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Music library unavailable"})
		return
	}
	a.melt.Event("roadtracks_search", map[string]any{"qlen": len(q), "count": len(albums), "status": http.StatusOK})
	_ = json.NewEncoder(w).Encode(map[string]any{"albums": albums})
}
