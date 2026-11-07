package main

// POST /driver/telemetry: the phone's own events for the MELT bus. A closed
// allowlist of event names and field keys; anything else is a 400. No
// coordinate key is accepted anywhere in the body, and the page cannot send
// one either (hud.js telemetryBody). 20 events a second per signed-in user.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	teleRate    = 20
	teleMaxBody = 4 << 10
)

// teleKinds: event -> field -> kind (s string, b bool, i int).
var teleKinds = map[string]map[string]byte{
	"nav_tap":    {"app": 's', "mode": 's'},
	"nav_result": {"app": 's', "mode": 's', "hidden": 'b'},
	"geo_error":  {"code": 'i'},
	"js_error":   {"msg": 's', "src": 's', "line": 'i'},
	"page_view":  {"path": 's'},
}

var teleEnums = map[string]map[string]bool{
	"app":  {"waze": true, "google": true, "apple": true},
	"mode": {"trip": true, "destined": true},
}

// teleLimiter is a token bucket per user, in memory. The zero value works.
type teleLimiter struct {
	mu sync.Mutex
	b  map[string]*teleBucket
}

type teleBucket struct {
	tokens float64
	at     time.Time
}

func (l *teleLimiter) allow(user string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.b == nil {
		l.b = map[string]*teleBucket{}
	}
	b := l.b[user]
	if b == nil {
		b = &teleBucket{tokens: teleRate, at: now}
		l.b[user] = b
	}
	b.tokens += now.Sub(b.at).Seconds() * teleRate
	if b.tokens > teleRate {
		b.tokens = teleRate
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// hasCoordKey reports a coordinate-shaped key at any depth.
func hasCoordKey(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			switch strings.ToLower(k) {
			case "lat", "lng", "latitude", "longitude", "coords":
				return true
			}
			if hasCoordKey(c) {
				return true
			}
		}
	case []any:
		for _, c := range x {
			if hasCoordKey(c) {
				return true
			}
		}
	}
	return false
}

func (a *App) telemetry(w http.ResponseWriter, r *http.Request) {
	user := a.userOf(r)
	// Accept sendBeacon's text/plain as well as application/json: the body is
	// JSON either way.
	raw, err := io.ReadAll(io.LimitReader(r.Body, teleMaxBody+1))
	if err != nil || len(raw) > teleMaxBody {
		http.Error(w, "body too large", http.StatusBadRequest)
		return
	}
	var body struct {
		Event  string         `json:"event"`
		Fields map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if hasCoordKey(body.Fields) {
		http.Error(w, "no coordinates", http.StatusBadRequest)
		return
	}
	spec, ok := teleKinds[body.Event]
	if !ok {
		http.Error(w, "unknown event", http.StatusBadRequest)
		return
	}
	clean := map[string]any{}
	for k, v := range body.Fields {
		kind, ok := spec[k]
		if !ok {
			http.Error(w, "unknown field "+k, http.StatusBadRequest)
			return
		}
		switch kind {
		case 's':
			s, ok := v.(string)
			if !ok {
				http.Error(w, "field "+k+" must be a string", http.StatusBadRequest)
				return
			}
			if enum := teleEnums[k]; enum != nil && !enum[s] {
				http.Error(w, "field "+k+" has a value not allowed", http.StatusBadRequest)
				return
			}
			clean[k] = s
		case 'b':
			b, ok := v.(bool)
			if !ok {
				http.Error(w, "field "+k+" must be a boolean", http.StatusBadRequest)
				return
			}
			clean[k] = b
		case 'i':
			f, ok := v.(float64)
			if !ok || f != float64(int64(f)) {
				http.Error(w, "field "+k+" must be an integer", http.StatusBadRequest)
				return
			}
			clean[k] = int64(f)
		}
	}
	if s, ok := clean["msg"].(string); ok && len(s) > 200 {
		clean["msg"] = s[:200]
	}
	for _, k := range []string{"src", "path"} {
		if s, ok := clean[k].(string); ok {
			if i := strings.IndexAny(s, "?#"); i >= 0 {
				s = s[:i]
			}
			if len(s) > 200 {
				s = s[:200]
			}
			clean[k] = s
		}
	}
	if !a.tele.allow(user, time.Now()) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return
	}
	clean["user"] = user
	clean["origin"] = "phone"
	a.melt.Event(body.Event, clean)
	w.WriteHeader(http.StatusNoContent)
}
