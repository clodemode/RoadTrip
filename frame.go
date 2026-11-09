package main

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// The player shell and the frame. A page load destroys the one <audio>
// element, so while the ride is Active a signed-in user's page is a shell: the
// Drive Tracks card, the audio element and one iframe that holds the real
// page (Kris, 2026-11-09: music plays on between screens). Everything below
// is server side; static/js/frame.js is the in-frame half.

// embedded reports whether a request is for the page inside the frame: the
// shell asks with embed=1, and a browser that sends Sec-Fetch-Dest says
// "iframe" for every navigation made in the frame, redirects after a post
// included, so a link needs no rewriting there.
func embedded(r *http.Request) bool {
	return r.URL.Query().Get("embed") == "1" || r.Header.Get("Sec-Fetch-Dest") == "iframe"
}

// dropEmbed removes embed=1 from a relative address, so a sign-in "next" or a
// shell address never carries the frame's own flag to the top window.
func dropEmbed(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Query().Get("embed") == "" {
		return raw
	}
	q := u.Query()
	q.Del("embed")
	u.RawQuery = q.Encode()
	return u.String()
}

// addEmbed puts embed=1 on a relative same-site address. Anything with another
// host, a static or media path, or no path is left as it was.
func addEmbed(raw, host string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Host != "" && !strings.EqualFold(u.Host, host)) || u.Path == "" ||
		strings.HasPrefix(u.Path, "/static/") || strings.HasPrefix(u.Path, "/media/") {
		return raw
	}
	q := u.Query()
	if q.Get("embed") == "1" {
		return raw
	}
	q.Set("embed", "1")
	u.RawQuery = q.Encode()
	return u.String()
}

// framed is the response writer of an embedded request: a redirect it sends
// stays embedded, so a post from the frame lands back in the frame.
type framed struct {
	http.ResponseWriter
	host string
}

func (f *framed) WriteHeader(code int) {
	if code >= 300 && code < 400 {
		if loc := f.Header().Get("Location"); loc != "" {
			f.Header().Set("Location", addEmbed(loc, f.host))
		}
	}
	f.ResponseWriter.WriteHeader(code)
}
func (f *framed) Flush() {
	if fl, ok := f.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}
func (f *framed) Unwrap() http.ResponseWriter { return f.ResponseWriter }

// frameGuard says only this site may frame any answer, and keeps a redirect
// made for the frame inside it.
func frameGuard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
		if embedded(r) {
			w = &framed{ResponseWriter: w, host: r.Host}
		}
		h.ServeHTTP(w, r)
	})
}

var shellPostEdit = regexp.MustCompile(`^/driver/post/\d+/edit$`)

// shellPage reports whether a GET path is one of the site's HTML pages, which
// the shell may wrap. Data, media and the sign-in page are never wrapped.
func shellPage(p string) bool {
	switch p {
	case "/", "/plan", "/photos", "/checklist", "/chester", "/driver", "/driver/post",
		"/driver/lodging", "/driver/chester/log", "/driver/chester/memory", "/driver/users":
		return true
	}
	return strings.HasPrefix(p, "/legs/") || shellPostEdit.MatchString(p)
}

// shell answers a signed-in page request with the player shell while the ride
// is Active and the music library is on. Visitors, Planning, the frame's own
// requests and every non-page path pass through untouched.
func (a *App) shell(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || a.tracks == nil || embedded(r) || !shellPage(r.URL.Path) || !a.auth.IsAuthed(r) {
			h.ServeHTTP(w, r)
			return
		}
		rv, err := a.loadRide()
		if err != nil || !rideActive(rv.Legs) {
			h.ServeHTTP(w, r)
			return
		}
		q := r.URL.Query()
		q.Set("embed", "1")
		src := r.URL.Path + "?" + q.Encode()
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Add("Vary", "Cookie")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := map[string]any{"V": a.assetV, "FrameSrc": src, "Resume": a.resumeFor(r), "RideName": rv.Ride.Name}
		if err := a.tmpl.ExecuteTemplate(w, "shell.html", data); err != nil {
			http.Error(w, "template error", http.StatusInternalServerError)
		}
	})
}
