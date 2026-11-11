package main

// The player's trouble report. The phone posts a few fixed words and numbers
// when a stream will not start, stalls or fails, and once when the first sound
// plays. Nothing else from the request is recorded: no browser name, no
// address, no typed words and no position. The record goes to MELT (the file
// sink on Fly), read back on GET /driver/melt.json.

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// roadtracksEvents is the whole list of words the phone may report.
var roadtracksEvents = map[string]bool{
	"first_sound": true, "play_timeout": true, "stream_error": true, "stream_stalled": true,
	"album_fetch_failed": true, "nest_cold": true, "ask_timeout": true, "card_missing": true,
}

var roadtracksEventCode = regexp.MustCompile(`^[a-z0-9_]{1,24}$`)

// formInt is the field as an integer when it is one within [lo, hi].
func formInt(r *http.Request, key string, lo, hi int64) (int64, bool) {
	v, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue(key)), 10, 64)
	if err != nil || v < lo || v > hi {
		return 0, false
	}
	return v, true
}

// roadtracksEvent: POST /driver/roadtracks/event. 204 once parsed, 400 for an
// unknown event. A bad optional field is dropped, never an error.
func (a *App) roadtracksEvent(w http.ResponseWriter, r *http.Request) {
	ev := strings.TrimSpace(r.PostFormValue("event"))
	if !roadtracksEvents[ev] {
		http.Error(w, "unknown event", http.StatusBadRequest)
		return
	}
	f := map[string]any{"origin": "phone"}
	if v, ok := formInt(r, "ms", 0, 600000); ok {
		f["ms"] = v
	}
	if c := strings.TrimSpace(r.PostFormValue("code")); roadtracksEventCode.MatchString(c) {
		f["code"] = c
	}
	if v, ok := formInt(r, "album_id", 1, 1<<53); ok {
		f["album_id"] = v
	}
	if v, ok := formInt(r, "track_idx", 0, 999); ok {
		f["track_idx"] = v
	}
	if ev == "first_sound" {
		a.melt.Event(ev, f)
	} else {
		a.melt.Error(ev, f)
	}
	w.WriteHeader(http.StatusNoContent)
}
