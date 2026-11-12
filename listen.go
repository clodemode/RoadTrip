package main

// Listen Inn audio: a signed-out visitor may hear the tracks that were played
// on the active ride (Kris, 2026-11-12: "Let them hear it"; scope reading in
// ruling-roadtrip-signed-out-visitors-may-hear-the-music-in-listen-inn-only-
// the-tracks-played-on-the-active-ride). One route, GET /tracks/{id}/audio,
// where {id} is the opaque public name of a plays row. It checks that the
// switch is on, that the caller is within the brake, and that the row belongs
// to the active ride; then it asks the library, server-side, with the key,
// for exactly that album's track list and answers 302 to the one address the
// library gave for exactly that track (a short-lived signed address that carries
// no key). No audio passes through this machine, the key never leaves it, and
// a visitor can name no track that was not played: there is no search and no
// album number here.

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	listenLimit  = 30          // audio requests one address may make...
	listenWindow = time.Minute // ...in this long
)

// listenOn is whether a visitor may hear the tracks: the setting is on
// (PUBLIC_LISTEN, default on) and the library is configured.
func (a *App) listenOn() bool { return !a.listenOff && a.tracks != nil }

// addrLimiter is a fixed window per address, in memory (the app is one process).
type addrLimiter struct {
	mu sync.Mutex
	n  map[string]int
	at time.Time // start of the current window
}

func (l *addrLimiter) allow(addr string, now time.Time, limit int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n == nil || now.Sub(l.at) >= window {
		l.n, l.at = map[string]int{}, now // one reset a window also bounds the map
	}
	l.n[addr]++
	return l.n[addr] <= limit
}

// clientAddr is the caller's address for the brake only; it is never stored or logged.
// Fly's edge sets Fly-Client-IP.
func clientAddr(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("Fly-Client-IP")); v != "" {
		return v
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// PlayAudioRef is what a play names in the library: server-side only.
type PlayAudioRef struct {
	AlbumID  int64
	TrackIdx int
}

// PlayOf finds a play of one ride by its opaque public name.
func (s *Store) PlayOf(ride, pub string) (PlayAudioRef, bool) {
	var ref PlayAudioRef
	if pub == "" {
		return ref, false
	}
	err := s.db.QueryRow(`SELECT album_id, track_idx FROM plays WHERE ride_slug = ? AND pub_id = ?`, ride, pub).Scan(&ref.AlbumID, &ref.TrackIdx)
	return ref, err == nil
}

func listenText(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(msg + "\n"))
}

// tracksAudio: GET /tracks/{id}/audio, open to a visitor like /tracks.json.
func (a *App) tracksAudio(w http.ResponseWriter, r *http.Request) {
	if !a.listenOn() {
		http.NotFound(w, r)
		return
	}
	if !a.listenBrake.allow(clientAddr(r), time.Now(), listenLimit, listenWindow) {
		w.Header().Set("Retry-After", "60")
		listenText(w, http.StatusTooManyRequests, "Lots of listening just now. Try again in a minute.")
		return
	}
	ref, ok := a.store.PlayOf(a.activeSlug(), r.PathValue("id"))
	if !ok {
		listenText(w, http.StatusNotFound, "That track is not on this trip's list.")
		return
	}
	t0 := time.Now()
	doc, err := a.tracks.album(r.Context(), ref.AlbumID)
	if err != nil {
		a.melt.Error("listen_audio", map[string]any{"status": http.StatusBadGateway, "ms": time.Since(t0).Milliseconds(), "reason": tracksReason(err)})
		listenText(w, http.StatusBadGateway, "The music library is not answering right now.")
		return
	}
	if ref.TrackIdx < 0 || ref.TrackIdx >= len(doc.Tracks) || doc.Tracks[ref.TrackIdx].Src == nil {
		listenText(w, http.StatusNotFound, "That track is not available to hear right now.")
		return
	}
	// One event per listen start; no address and no place.
	a.melt.Event("listen_audio", map[string]any{"status": http.StatusFound, "ms": time.Since(t0).Milliseconds()})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, *doc.Tracks[ref.TrackIdx].Src, http.StatusFound)
}
