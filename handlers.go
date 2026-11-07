package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// legColors is the polyline colour cycle; leg N takes legColors[(N-1) % len].
var legColors = []string{"#2563eb", "#dc2626", "#059669", "#d97706", "#7c3aed", "#db2777"}

func legColor(ord int) string {
	if ord < 1 {
		ord = 1
	}
	return legColors[(ord-1)%len(legColors)]
}

// postKinds is the whole taxonomy. Recommendations and mishaps are kinds,
// not pages — the spec ruled that.
var postKinds = []struct{ Value, Label string }{
	{"note", "Note"}, {"photo", "Photo"}, {"sight", "Sight"}, {"meal", "Meal"},
	{"lodging", "Lodging"}, {"mishap", "Mishap"}, {"tip", "Tip"},
}

func validKind(k string) bool {
	for _, pk := range postKinds {
		if pk.Value == k {
			return true
		}
	}
	return false
}

const (
	fmtLocal = "Mon Jan 2 · 3:04 PM MST"
	fmtClock = "3:04 PM MST"
	fmtDay   = "Mon Jan 2"
	// fmtInput is what <input type="datetime-local"> both renders and posts.
	// It carries no zone of its own — the zone is whatever the form's Zone
	// select says — which is why the edit screen re-reads that wall clock in
	// the chosen zone instead of shifting the stored instant.
	fmtInput = "2006-01-02T15:04"
)

// localFmt renders a stored UTC timestamp in an IANA zone. Every time on the
// site goes through here: UTC in the database, local on the page.
func localFmt(utc, tz, layout string) string {
	t, ok := parseUTC(utc)
	if !ok {
		return ""
	}
	return t.In(loadLoc(tz)).Format(layout)
}

func (a *App) funcs() template.FuncMap {
	return template.FuncMap{
		"waze":       func(name string, lat, lng float64) string { return wazeLink(name, lat, lng) },
		"gmaps":      func(name string, lat, lng float64) string { return mapLinks(lat, lng, name).Google },
		"applemaps":  func(name string, lat, lng float64) string { return mapLinks(lat, lng, name).Apple },
		"askChester": askChesterLink,
		"json": func(v any) template.JS {
			b, err := json.Marshal(v)
			if err != nil {
				return template.JS("null")
			}
			return template.JS(b)
		},
		"local": func(utc, tz string) string { return localFmt(utc, tz, fmtLocal) },
		"clock": func(utc, tz string) string { return localFmt(utc, tz, fmtClock) },
		// wall is a stored time as a datetime-local input wants it, in its zone.
		"wall": func(utc, tz string) string { return localFmt(utc, tz, fmtInput) },
		"day": func(d string) string {
			t, err := time.Parse("2006-01-02", d)
			if err != nil {
				return d
			}
			return t.Format(fmtDay)
		},
		"card":       func(p Post, authed bool) postCard { return postCard{Post: p, Authed: authed} },
		"legColor":   legColor,
		"trimPrefix": func(s, prefix string) string { return strings.TrimPrefix(s, prefix) },
		"num":        func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) },
		"inc":        func(i int) int { return i + 1 },
		// mi and hr write a planner figure to one decimal, always.
		"mi": fmtMiles,
		"hr": fmtMiles,
		// Chester's log: dollars to four places, a duration in seconds, a
		// figure that may be absent, a percentage.
		"usd":  usd4,
		"usd2": usd2,
		"secs": func(ms int64) string { return strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64) + " s" },
		"opt": func(f *float64) string {
			if f == nil {
				return ""
			}
			return strconv.FormatFloat(*f, 'f', -1, 64)
		},
		"pct": func(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) + "%" },
	}
}

// --- routing -------------------------------------------------------------------

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/",
		cacheControl(http.FileServer(http.FS(sub)), "public, max-age=31536000, immutable")))
	// Media paths are content-addressed (sha256 prefix), so they are immutable
	// by construction and can be cached as hard as the static assets.
	mux.Handle("GET /media/", http.StripPrefix("/media/",
		cacheControl(noIndex(http.FileServer(http.Dir(a.mediaDir))), "public, max-age=31536000, immutable")))

	// robots.txt answers even while the site is private, so a crawler that
	// arrives early is told what not to take rather than handed a redirect to
	// a login page. /media/ is excluded because an original is a photo, not a
	// document: it should not turn up in an image search, and a file that has
	// been indexed cannot be un-indexed on the day someone changes their mind.
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "User-agent: *\nDisallow: /driver/\nDisallow: /media/\nAllow: /\n")
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("GET /{$}", a.index)
	mux.HandleFunc("GET /legs/{slug}", a.legPage)
	mux.HandleFunc("GET /photos", a.photos)
	// Driver-only (Kris, 2026-11-07): the cookie or the PUSH_TOKEN bearer.
	mux.Handle("GET /route.geojson", a.requireRide(a.routeGeoJSON))
	// The planner: the active ride's days and, for a trip with options, every
	// alternative. /plan.json is the same arithmetic as data.
	mux.HandleFunc("GET /plan", a.planPage)
	mux.HandleFunc("GET /plan.json", a.planJSON)
	// The checklist: the active ride's steps and questions. Private text is
	// read only for a signed-in request; /checklist.json never carries it.
	// Both are driver-only (Kris, 2026-11-07).
	mux.Handle("GET /checklist", a.requireRide(a.checklistPage))
	mux.Handle("GET /checklist.json", a.requireRide(a.checklistJSON))

	mux.HandleFunc("GET /driver/login", a.loginForm)
	mux.HandleFunc("POST /driver/login", a.loginSubmit)
	mux.HandleFunc("POST /driver/logout", a.logout)
	mux.Handle("GET /driver", a.requireAuth(a.driverHome))
	mux.Handle("GET /driver/hud.json", a.requireAuth(a.hudJSON)) // gates itself: FreeLance has no ride
	mux.Handle("POST /driver/freelance/mode", a.requireAuth(a.freeMode))
	mux.Handle("POST /driver/freelance/dest", a.requireAuth(a.freeDest))
	mux.Handle("POST /driver/freelance/picks", a.requireAuth(a.freePicks))
	mux.Handle("GET /driver/users", a.requireAdmin(a.usersPage))
	mux.Handle("POST /driver/users", a.requireAdmin(a.usersCreate))
	mux.Handle("POST /driver/users/{name}/reset", a.requireAdmin(a.usersReset))
	mux.Handle("POST /driver/users/{name}/disable", a.requireAdmin(a.usersDisable))
	mux.Handle("POST /driver/users/{name}/enable", a.requireAdmin(a.usersEnable))
	mux.Handle("POST /driver/users/{name}/rides", a.requireAdmin(a.usersRides))
	mux.Handle("GET /driver/post", a.requireRide(a.postForm))
	mux.Handle("POST /driver/post", a.requireRide(a.postCreate))
	mux.Handle("POST /driver/checkpoint/{id}/arrive", a.requireRide(a.checkpointArrive))
	mux.Handle("POST /driver/post/{id}/delete", a.requireRide(a.postDelete))
	// The census: every post, for finding duplicates the road left behind.
	mux.Handle("GET /driver/posts.json", a.requireRide(a.postsJSON))
	mux.Handle("POST /driver/post/{id}/pin", a.requireRide(a.postRepin))
	mux.Handle("GET /driver/post/{id}/edit", a.requireRide(a.postEditForm))
	mux.Handle("POST /driver/post/{id}/edit", a.requireRide(a.postEditSave))
	mux.Handle("GET /driver/lodging", a.requireRide(a.lodgingForm))
	mux.Handle("POST /driver/lodging", a.requireRide(a.lodgingSave))
	// The data carriage's landing point. Same admin session as every other
	// /driver route — the carriage signs in the way a person does, so there is
	// no second credential and no second auth path to get wrong.
	mux.Handle("POST /driver/private", a.requireAuth(a.privatePush))
	// The trip carriage and its switches: a trip document onto the volume,
	// which ride the site shows, and the saved selection. Same gate as above.
	mux.Handle("POST /driver/trip", a.requireAuth(a.tripPush))
	mux.Handle("POST /driver/ride/active", a.requireAuth(a.rideActivate))
	mux.Handle("GET /driver/rides.json", a.requireAuth(a.ridesJSON))
	mux.Handle("POST /driver/plan", a.requireRide(a.planSave))
	// The checklist carriage, its signed-in census, and the check-off.
	mux.Handle("POST /driver/checklist", a.requireAuth(a.checklistPush))
	mux.Handle("GET /driver/checklist.json", a.requireRide(a.checklistCensus))
	mux.Handle("POST /driver/checklist/{kind}/{id}", a.requireRide(a.checklistMark))
	// Chester, the en-route helper, and its log. Signed in only, every one:
	// a question spends money and the log is the trip-keeper's own.
	mux.Handle("GET /chester", a.requireAuth(a.chesterPage))
	mux.Handle("POST /chester/ask", a.requireRide(a.chesterAsk))
	mux.Handle("GET /driver/chester/log", a.requireAuth(a.chesterLog))
	mux.Handle("GET /driver/chester/log.json", a.requireAuth(a.chesterLogJSON))
	mux.Handle("POST /driver/chester/log/{id}", a.requireAuth(a.chesterVerdict))

	return logRequests(a.canonicalHost(a.visibility(mux)))
}

// visibilityOpen is the allowlist that stays reachable while the whole site is
// private. It is load-bearing, not a convenience:
//
//   - /healthz answers Fly's health check, which arrives with an arbitrary
//     Host. Redirect it and the machine is marked unhealthy and stops serving.
//   - the login page itself, or there is no way in.
//   - exactly the two static files that page renders with: its stylesheet and
//     the creature, which is both the masthead image and the favicon. Not
//     /static wholesale.
//   - /robots.txt, so a crawler that arrives while the site is closed reads
//     the exclusions instead of a 302 to the login form.
var visibilityOpen = map[string]bool{
	"/healthz":                 true,
	"/robots.txt":              true,
	"/driver/login":            true,
	"/static/css/app.css":      true,
	"/static/img/creature.svg": true,
}

// visibility is the temporary whole-site gate: with SITE_VISIBILITY=private
// every page, the GeoJSON included, requires the one admin session. Kris ruled
// it on 2026-09-02 ("we will open it later"), so it is a config flip in
// fly.toml [env] and a redeploy — never a code change. Default is public.
//
// It reuses the existing session auth and the one admin password. There is no
// second credential and no Basic auth. It runs INSIDE canonicalHost so a
// request on the wrong host is still 301'd to the public name first.
func (a *App) visibility(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.sitePrivate || visibilityOpen[r.URL.Path] || a.auth.IsAuthed(r) {
			h.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, "/driver/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
	})
}

// canonicalHost 301s any request whose Host is not SITE_URL's host to the same
// path+query on SITE_URL, so the .fly.dev host (and anything else pointed at
// the app) collapses onto the one public name. Off when SITE_URL is empty.
// /healthz is exempt: Fly's health check reaches the machine with an arbitrary
// Host and must keep answering 200 or the machine is marked unhealthy.
func (a *App) canonicalHost(h http.Handler) http.Handler {
	if a.siteURL == "" {
		return h
	}
	u, err := url.Parse(a.siteURL)
	if err != nil || u.Host == "" {
		return h
	}
	want := strings.ToLower(u.Host)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || strings.ToLower(r.Host) == want {
			h.ServeHTTP(w, r)
			return
		}
		target := a.siteURL + r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

func cacheControl(h http.Handler, v string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", v)
		h.ServeHTTP(w, r)
	})
}

// noIndex refuses directory listings under /media/.
func noIndex(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") || r.URL.Path == "" {
			http.NotFound(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		h.ServeHTTP(w, r)
	})
}

// requireRide is requireAuth plus a ride check: a driver is on the ride the
// request is for or gets a 403. The ride is ?ride= when named, else the ride the
// site shows. An admin and the push token are on every ride. A write that names
// its ride in the form (a checklist mark) is checked again where the form is read.
func (a *App) requireRide(next http.HandlerFunc) http.Handler {
	return a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		slug := a.activeSlug()
		if v := strings.TrimSpace(r.URL.Query().Get("ride")); v != "" {
			slug = v
		}
		if !a.auth.mayRide(r, slug, a.activeSlug()) {
			a.refuseRide(w, r)
			return
		}
		next(w, r)
	})
}

func (a *App) refuseRide(w http.ResponseWriter, r *http.Request) {
	log.Printf("refused: %s %s, %s is not assigned to that ride", r.Method, r.URL.Path, a.userOf(r))
	http.Error(w, "not your ride: an admin has not assigned you to it", http.StatusForbidden)
}

// requireAdmin is requireAuth plus a role check: a signed-in editor, and the
// push token (a machine, not a person), get a 403.
func (a *App) requireAdmin(next http.HandlerFunc) http.Handler {
	return a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if !a.auth.IsAdmin(r) {
			http.Error(w, "admins only", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

func (a *App) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.auth.IsAuthed(r) {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, "/driver/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
				return
			}
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		// A write that rides on the cookie must come from this site's own
		// pages. The cookie is SameSite=Lax, which stops another site's form
		// but not a page on a sibling host of the same site: the browser
		// sends the cookie with that page's POST, and here that could write
		// to the trip or spend Chester's money. The push token is not a
		// cookie and no browser attaches it, so that path is left alone.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !a.auth.pushAuthed(r) && !a.ownOrigin(r) {
			log.Printf("refused: a signed-in %s %s came from another origin", r.Method, r.URL.Path)
			http.Error(w, "this request came from another site", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ownOrigin reports whether a request may have come from one of this site's
// own pages, by its Origin header. A browser sends Origin with every POST a
// page makes, form or fetch, and a page cannot forge it. When the header is
// there its host must be the host the request was sent to or SITE_URL's host;
// anything else, "null" included, is another origin. A request with no
// Origin header is not a browser's cross-origin write and passes unchanged.
func (a *App) ownOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	if host == strings.ToLower(r.Host) {
		return true
	}
	if a.siteURL != "" {
		if site, err := url.Parse(a.siteURL); err == nil && site.Host != "" && host == strings.ToLower(site.Host) {
			return true
		}
	}
	return false
}

func (a *App) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	data["V"] = a.assetV
	authed := a.auth.IsAuthed(r)
	data["Authed"] = authed
	data["IsAdmin"] = a.auth.IsAdmin(r)
	// Driver: signed in AND on the ride the site shows. The ride's tools (post,
	// lodging, checklist, GeoJSON, plan controls) are drawn only for these.
	data["Driver"] = authed && a.auth.mayRide(r, a.activeSlug(), a.activeSlug())
	if authed {
		// Every page goes through here, and this is where the session is known.
		// A signed-in page is served on the SAME address a visitor uses and can
		// carry what a visitor must never see (a private note, an address, the
		// controls), so no cache may keep it or hand it to anyone else. Set
		// once, here, so a new page cannot forget it. The anonymous response is
		// left exactly as it was.
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Add("Vary", "Cookie")
	}
	// SiteURL/Canonical drive <link rel=canonical> and the og:* tags; both are
	// empty (and the tags absent) when SITE_URL is unset. Canonical is the
	// page's own address on the public host — path only, no query.
	data["SiteURL"] = a.siteURL
	data["Canonical"] = ""
	if a.siteURL != "" {
		data["Canonical"] = a.siteURL + r.URL.Path
	}
	data["SitePrivate"] = a.sitePrivate
	// Whether the masthead links to /checklist: only for a ride that has one.
	_, data["HasChecklist"] = a.store.ChecklistSummary(a.activeSlug())
	// Whether the masthead links to Chester: signed in, and switched on.
	data["ChesterOn"] = authed && a.chester != nil
	data["CreatureName"] = CreatureName
	data["CreatureTag"] = CreatureTag
	data["CreatureAlt"] = CreatureAlt
	data["GuideName"] = GuideName
	data["GuideTag"] = GuideTag
	// PageLeg picks the southwest palette and the horizon band: the body's
	// data-leg attribute. Pages that have no leg of their own (sign-in) take
	// the first leg, which doubles as the ride's overall look.
	if _, ok := data["PageLeg"]; !ok {
		data["PageLeg"] = ""
		if legs, err := a.store.Legs(a.activeSlug()); err == nil && len(legs) > 0 {
			data["PageLeg"] = legs[0].Slug
		}
	}
	if _, ok := data["Ride"]; !ok {
		if ride, err := a.store.Ride(a.activeSlug()); err == nil {
			data["Ride"] = ride
		} else {
			data["Ride"] = &Ride{Name: "Roadtrip"}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("ERROR render %s: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// --- the ride, as the pages see it ----------------------------------------------

type rideView struct {
	Ride *Ride
	Legs []Leg
}

// loadRide reads the ACTIVE ride (activeSlug), which is why every page, the
// post form and the open data all move together when the trip-keeper switches
// rides, and why none of them can show a ride the others do not.
func (a *App) loadRide() (*rideView, error) {
	ride, err := a.store.Ride(a.activeSlug())
	if err != nil {
		return nil, err
	}
	legs, err := a.store.Legs(ride.Slug)
	if err != nil {
		return nil, err
	}
	followArrivals(legs)
	markCheckpoints(legs)
	return &rideView{Ride: ride, Legs: legs}, nil
}

// legTerminus and legOrigin name the two checkpoints a leg's line actually
// runs between. The boundary checkpoint belongs to the EARLIER leg — leg 2
// starts where leg 1 ended, at leg 1's last checkpoint — so a leg's origin is
// the previous leg's terminus, and only leg 1 begins at one of its own.
func legTerminus(l Leg) *Checkpoint {
	if n := len(l.Checkpoints); n > 0 {
		return &l.Checkpoints[n-1]
	}
	return nil
}

func legOrigin(legs []Leg, i int) *Checkpoint {
	if i > 0 {
		if c := legTerminus(legs[i-1]); c != nil {
			return c
		}
	}
	if len(legs[i].Checkpoints) > 0 {
		return &legs[i].Checkpoints[0]
	}
	return nil
}

// followArrivals bends the planned polylines to where the car actually got to:
// a leg whose terminus has been stamped ENDS at that checkpoint's published
// point, and the next leg starts there too. It runs in loadRide, which is the
// whole reason it is here rather than in each handler — /, /legs/… and
// /route.geojson all read the ride through this one function, so they cannot
// end up disagreeing about where a day finished.
//
// It moves endpoints and nothing else: the shape in between is still the plan,
// because moving a pin is a fact and re-routing the day is a guess. A fuzzed
// checkpoint contributes its snapped point — the only point the store ever
// hands out — so a private place stays a cell here too.
func followArrivals(legs []Leg) {
	for i := range legs {
		var pts [][]float64
		if json.Unmarshal(legs[i].Line, &pts) != nil || len(pts) < 2 {
			continue
		}
		moved := false
		if c := legOrigin(legs, i); c != nil && c.ArrivedAt != "" {
			pts[0] = []float64{c.Lng, c.Lat}
			moved = true
		}
		if c := legTerminus(legs[i]); c != nil && c.ArrivedAt != "" {
			pts[len(pts)-1] = []float64{c.Lng, c.Lat}
			moved = true
		}
		if !moved {
			continue
		}
		if b, err := json.Marshal(pts); err == nil {
			legs[i].Line = b
		}
	}
}

// legTracks is the line the car is known to have actually driven, per leg: the
// arrived origin, then that leg's pinned posts in the order they happened,
// then the arrived terminus. It is deliberately not the planned polyline —
// the plan is intent, this is evidence — so a leg with nothing actual yet has
// no track at all and the map shows the plan alone.
//
// One exception, and it is marked as one (plannedStarts): a leg that has a
// pinned post but whose origin nobody stamped still starts its track at that
// origin. A photo from the road says the day began, and the day began where
// the leg begins; without this the first photo of a morning was a lone pin
// beside a plan that still looked untravelled. The origin is the checkpoint's
// PUBLISHED point, the one the store hands out — a snapped cell at a private
// place — and nothing is stamped arrived on the checkpoint itself.
//
// Two points is the floor, and not only because a one-point LineString is
// invalid GeoJSON: one fix is a pin, not a track. A leg with no pinned post
// and no two arrivals still has none.
//
// It is keyed by leg ID and computed over the WHOLE ride, because a leg's
// origin lives on the leg before it — the leg page, which holds one leg, would
// otherwise lose the point its day started from.
func legTracks(legs []Leg, posts []Post) map[int64][][]float64 {
	lines, _ := driven(legs, posts)
	return lines
}

// plannedStarts names the legs whose track begins at an origin that was never
// stamped arrived: the first vertex of such a track is the plan, and every
// vertex after it is evidence. The map draws that first stretch lighter and
// the open data says so, so a track never claims more than is known.
func plannedStarts(legs []Leg, posts []Post) map[int64]bool {
	_, planned := driven(legs, posts)
	return planned
}

func driven(legs []Leg, posts []Post) (lines map[int64][][]float64, planned map[int64]bool) {
	byLeg := map[int64][]Post{}
	for _, p := range posts {
		if p.PinLat != nil {
			byLeg[p.LegID] = append(byLeg[p.LegID], p)
		}
	}
	lines, planned = map[int64][][]float64{}, map[int64]bool{}
	for i := range legs {
		var pts [][]float64
		pinned := byLeg[legs[i].ID]
		sort.SliceStable(pinned, func(x, y int) bool {
			return pinned[x].OccurredAt < pinned[y].OccurredAt
		})
		origin, term := legOrigin(legs, i), legTerminus(legs[i])
		plannedStart := false
		switch {
		case origin == nil:
		case origin.ArrivedAt != "":
			pts = append(pts, []float64{origin.Lng, origin.Lat})
		case len(pinned) > 0 && (term == nil || origin.ID != term.ID):
			// Unstamped, but the leg has a photo on it. A leg whose only
			// checkpoint is its own end has no origin to start from.
			pts = append(pts, []float64{origin.Lng, origin.Lat})
			plannedStart = true
		}
		for _, p := range pinned {
			pts = append(pts, []float64{*p.PinLng, *p.PinLat})
		}
		if term != nil && term.ArrivedAt != "" {
			pts = append(pts, []float64{term.Lng, term.Lat})
		}
		if len(pts) >= 2 {
			lines[legs[i].ID] = pts
			if plannedStart {
				planned[legs[i].ID] = true
			}
		}
	}
	return lines, planned
}

// dayInfo is the hero badge. "Today" is judged in the zone each leg starts
// in, never in one ride-wide zone: the day rolls over at midnight where the
// car is, not where it left.
type dayInfo struct {
	Badge string
	Today *Leg // nil when no leg is dated today
	N     int  // day number, 1-based; 0 when not on the road
	Total int
}

func dayOf(ride *Ride, legs []Leg, now time.Time) dayInfo {
	d := dayInfo{Total: len(legs)}
	if len(legs) == 0 {
		d.Badge = "No legs"
		return d
	}
	for i := range legs {
		l := &legs[i]
		if now.In(loadLoc(l.TZ)).Format("2006-01-02") == l.Date {
			d.Today, d.N = l, i+1
			d.Badge = "Day " + strconv.Itoa(i+1) + " of " + strconv.Itoa(d.Total)
			return d
		}
	}
	originLoc := loadLoc(legs[0].TZ)
	today := now.In(originLoc)
	todayStr := today.Format("2006-01-02")
	if todayStr < ride.StartDate {
		start, err := time.ParseInLocation("2006-01-02", ride.StartDate, originLoc)
		if err == nil {
			days := int(start.Sub(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, originLoc)).Hours()/24 + 0.5)
			switch days {
			case 1:
				d.Badge = "Departs tomorrow"
			default:
				d.Badge = "Departs in " + strconv.Itoa(days) + " days"
			}
			return d
		}
		d.Badge = "Before the ride"
		return d
	}
	// Judged at the terminus: the ride is over when the last leg's day has
	// ended where the car ended up.
	last := legs[len(legs)-1]
	termLoc := loadLoc(last.TZ)
	if cps := last.Checkpoints; len(cps) > 0 {
		termLoc = loadLoc(cps[len(cps)-1].TZ)
	}
	if now.In(termLoc).Format("2006-01-02") > ride.EndDate {
		d.Badge = "Arrived"
		return d
	}
	d.Badge = "Day off"
	return d
}

// defaultLeg is where a new post lands: today's leg, else the first before
// the ride and the last after it.
func defaultLeg(legs []Leg, di dayInfo) *Leg {
	if di.Today != nil {
		return di.Today
	}
	if len(legs) == 0 {
		return nil
	}
	if di.Badge == "Arrived" || di.Badge == "Day off" {
		return &legs[len(legs)-1]
	}
	return &legs[0]
}

// defaultTZ follows the car: the zone of the most recently ARRIVED
// checkpoint, else the zone the default leg starts in. One tap on "Arrived"
// at Cloudcroft flips the form from CDT to MDT.
func defaultTZ(legs []Leg, dl *Leg) string {
	latest, tz := "", ""
	for _, l := range legs {
		for _, c := range l.Checkpoints {
			if c.ArrivedAt != "" && c.ArrivedAt > latest {
				latest, tz = c.ArrivedAt, c.TZ
			}
		}
	}
	if tz != "" {
		return tz
	}
	if dl != nil && dl.TZ != "" {
		return dl.TZ
	}
	return "UTC"
}

// zones lists the ride's zones, in checkpoint order, deduplicated.
func zones(legs []Leg, extra ...string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(z string) {
		if z != "" && !seen[z] {
			seen[z] = true
			out = append(out, z)
		}
	}
	for _, l := range legs {
		for _, c := range l.Checkpoints {
			add(c.TZ)
		}
	}
	for _, z := range extra {
		add(z)
	}
	return out
}

// --- map payload ---------------------------------------------------------------

type legJS struct {
	ID    int64           `json:"id"`
	Ord   int             `json:"ord"`
	Slug  string          `json:"slug"`
	Name  string          `json:"name"`
	Date  string          `json:"date"`
	Color string          `json:"color"`
	Line  json.RawMessage `json:"line"`
}

type cpJS struct {
	ID        int64   `json:"id"`
	Ord       int     `json:"ord"`
	Mark      string  `json:"mark"`
	Name      string  `json:"name"`
	Label     string  `json:"label"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	TZ        string  `json:"tz"`
	Precision string  `json:"precision"`
	LegSlug   string  `json:"leg_slug"`
	Planned   string  `json:"planned"` // local text
	Arrived   string  `json:"arrived"` // local text
}

type sightJS struct {
	Name string  `json:"name"`
	Why  string  `json:"why"`
	Kind string  `json:"kind"`
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
}

type postJS struct {
	ID      int64   `json:"id"`
	Kind    string  `json:"kind"`
	Title   string  `json:"title"`
	When    string  `json:"when"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Thumb   string  `json:"thumb,omitempty"`
	Full    string  `json:"full,omitempty"`
	LegSlug string  `json:"leg_slug"`
	// Where the pin came from: exif | browser | checkpoint. The popup says
	// "near <checkpoint>" for a checkpoint pin and "±N m" for a browser one.
	PinSource string  `json:"pin_source"`
	AccuracyM float64 `json:"accuracy_m,omitempty"`
	Near      string  `json:"near,omitempty"`
}

// trackJS is one leg's actual driven line (legTracks). The map draws the
// planned leg dimmed and dashed once its leg has one of these, so the plan
// stops competing with the evidence.
type trackJS struct {
	LegOrd int         `json:"leg"`
	Slug   string      `json:"slug"`
	Name   string      `json:"name"`
	Color  string      `json:"color"`
	Line   [][]float64 `json:"line"`
	// PlannedStart: the first vertex is the leg's planned origin, not a place
	// the ride was stamped at. The map draws that first stretch lighter.
	PlannedStart bool `json:"planned_start,omitempty"`
}

type mapJS struct {
	Legs        []legJS   `json:"legs"`
	Tracks      []trackJS `json:"tracks"`
	Checkpoints []cpJS    `json:"checkpoints"`
	Sights      []sightJS `json:"sights"`
	Posts       []postJS  `json:"posts"`
}

// buildMap takes tracks rather than deriving them: they are computed over the
// whole ride (a leg's origin sits on the leg before it) while this is called
// with one leg on a leg page. planned is plannedStarts over the same ride.
func buildMap(legs []Leg, posts []Post, tracks map[int64][][]float64, planned map[int64]bool) mapJS {
	m := mapJS{Legs: []legJS{}, Tracks: []trackJS{}, Checkpoints: []cpJS{}, Sights: []sightJS{}, Posts: []postJS{}}
	for _, l := range legs {
		m.Legs = append(m.Legs, legJS{ID: l.ID, Ord: l.Ord, Slug: l.Slug, Name: l.Name, Date: l.Date,
			Color: legColor(l.Ord), Line: l.Line})
		if pts := tracks[l.ID]; len(pts) > 0 {
			m.Tracks = append(m.Tracks, trackJS{LegOrd: l.Ord, Slug: l.Slug, Name: l.Name,
				Color: legColor(l.Ord), Line: pts, PlannedStart: planned[l.ID]})
		}
		for _, c := range l.Checkpoints {
			planned := ""
			if c.PlannedArrive != "" {
				planned = "arrive " + localFmt(c.PlannedArrive, c.TZ, fmtClock)
			} else if c.PlannedDepart != "" {
				planned = "depart " + localFmt(c.PlannedDepart, c.TZ, fmtClock)
			}
			m.Checkpoints = append(m.Checkpoints, cpJS{ID: c.ID, Ord: c.Ord, Mark: c.Mark, Name: c.Name, Label: c.Label,
				Lat: c.Lat, Lng: c.Lng, TZ: c.TZ, Precision: c.Precision, LegSlug: l.Slug,
				Planned: planned, Arrived: localFmt(c.ArrivedAt, c.TZ, fmtLocal)})
		}
		for _, s := range l.Sights {
			m.Sights = append(m.Sights, sightJS{Name: s.Name, Why: s.Why, Kind: s.Kind, Lat: s.Lat, Lng: s.Lng})
		}
	}
	for _, p := range posts {
		if p.PinLat == nil {
			continue
		}
		pj := postJS{ID: p.ID, Kind: p.Kind, Title: p.Title, When: localFmt(p.OccurredAt, p.TZ, fmtLocal),
			Lat: *p.PinLat, Lng: *p.PinLng, LegSlug: p.LegSlug, PinSource: p.PinSource}
		if p.AccuracyM != nil {
			pj.AccuracyM = *p.AccuracyM
		}
		if p.PinSource == "checkpoint" {
			pj.Near = p.CheckpointName
		}
		if p.Photo != nil {
			pj.Thumb = "/media/" + p.Photo.ThumbPath
			pj.Full = "/media/" + p.Photo.Path
		}
		m.Posts = append(m.Posts, pj)
	}
	return m
}

// --- public pages ----------------------------------------------------------------

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	rv, err := a.loadRide()
	if err != nil {
		http.Error(w, "ride "+a.activeSlug()+" is not seeded", http.StatusNotFound)
		return
	}
	posts, err := a.store.Posts(rv.Ride.Slug, 0)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	di := dayOf(rv.Ride, rv.Legs, time.Now())
	// The hero's terrain follows the car: today's leg, leg 1 before departure,
	// the last leg once the ride is over — the same rule a new post follows.
	pageLeg := ""
	if dl := defaultLeg(rv.Legs, di); dl != nil {
		pageLeg = dl.Slug
	}
	data := map[string]any{
		"Title": "", "Ride": rv.Ride, "Legs": rv.Legs, "Posts": posts,
		"Day": di, "PageLeg": pageLeg,
		"Map": buildMap(rv.Legs, posts, legTracks(rv.Legs, posts), plannedStarts(rv.Legs, posts))}
	// The checklist in one line, n of m done. Counts only.
	if sum, ok := a.store.ChecklistSummary(rv.Ride.Slug); ok {
		data["Checklist"] = sum
	}
	a.render(w, r, "index.html", data)
}

func (a *App) legPage(w http.ResponseWriter, r *http.Request) {
	rv, err := a.loadRide()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var leg *Leg
	var prev, next *Leg
	for i := range rv.Legs {
		if rv.Legs[i].Slug == r.PathValue("slug") {
			leg = &rv.Legs[i]
			if i > 0 {
				prev = &rv.Legs[i-1]
			}
			if i+1 < len(rv.Legs) {
				next = &rv.Legs[i+1]
			}
		}
	}
	if leg == nil {
		http.NotFound(w, r)
		return
	}
	posts, err := a.store.Posts(rv.Ride.Slug, leg.ID)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	di := dayOf(rv.Ride, rv.Legs, time.Now())
	a.render(w, r, "leg.html", map[string]any{
		"Title": leg.Name, "Ride": rv.Ride, "Leg": leg, "Prev": prev, "Next": next,
		"Posts": posts, "Day": di, "Total": len(rv.Legs),
		// The tracks are computed over the whole ride so this leg's own track
		// still begins where the day before it ended.
		"Map":     buildMap([]Leg{*leg}, posts, legTracks(rv.Legs, posts), plannedStarts(rv.Legs, posts)),
		"PageLeg": leg.Slug})
}

// postCard is what the post_card template is called with: the post, plus
// whether the viewer is signed in. Post is embedded so every existing field
// reference in the template (.Kind, .Photo, …) is unchanged. The template's
// pin line — raw coordinates and the fuzz decision — is behind .Authed, and
// an anonymous render never contains it.
type postCard struct {
	Post
	Authed bool
}

// PinLine is the signed-in-only account of one post's pin: what the photo
// carried, and what the site actually published. It exists because the app
// used to decide in silence — on 2026-09-02 Kris posted a photo that did have
// GPS, the pin landed on the fuzzed home checkpoint, and no surface said so.
// Rendered ONLY under .Authed (base.html post_card), so an anonymous render
// never contains a raw coordinate.
func (c postCard) PinLine() string {
	coord := func(lat, lng *float64) string { return fmt.Sprintf("%.4f, %.4f", *lat, *lng) }
	var raw []string
	if c.Photo != nil && c.Photo.Lat != nil && c.Photo.Lng != nil {
		raw = append(raw, "photo GPS "+coord(c.Photo.Lat, c.Photo.Lng))
	}
	if c.PinSource == pinBrowser && c.AccuracyM != nil {
		raw = append(raw, fmt.Sprintf("phone ±%.0f m", *c.AccuracyM))
	} else if c.PinSource == pinBrowser {
		raw = append(raw, "phone location")
	}
	var pub string
	switch {
	case c.HideLocation:
		pub = "nothing (location hidden)"
	case c.PinSource == pinNone || c.Lat == nil:
		pub = "no pin"
	case c.PinFuzzed && c.PinSource == pinCheckpoint && c.CheckpointName != "":
		// Only a fuzz that landed IN a private checkpoint's cell becomes a
		// checkpoint pin, and only that one may claim the name. A grid fuzz
		// somewhere with no checkpoint keeps its own source and stays nameless
		// even when the post is filed under a checkpoint.
		pub = "fuzzed, near " + c.CheckpointName
	case c.PinFuzzed:
		pub = "fuzzed to " + coord(c.Lat, c.Lng)
	case c.PinSource == pinCheckpoint:
		pub = c.CheckpointName + " at " + coord(c.Lat, c.Lng)
	default:
		pub = "exact " + coord(c.Lat, c.Lng)
	}
	line := "pin: "
	if len(raw) > 0 {
		line += strings.Join(raw, ", ") + " → "
	}
	return line + "published " + pub
}

// RepinCheckpoint is the name the re-pin control offers as the checkpoint
// option — empty when the post has no checkpoint to fall back to.
func (c postCard) RepinCheckpoint() string { return c.CheckpointName }

// RepinEXIF says whether this post's photo still carries a GPS position that
// could be re-chosen. A browser fix is not re-derivable and is not offered.
func (c postCard) RepinEXIF() bool {
	return c.Photo != nil && c.Photo.Lat != nil && c.Photo.Lng != nil
}

type legPhotos struct {
	Leg   Leg
	Posts []Post
}

func (a *App) photos(w http.ResponseWriter, r *http.Request) {
	rv, err := a.loadRide()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	posts, err := a.store.Posts(rv.Ride.Slug, 0)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	byLeg := map[int64][]Post{}
	total := 0
	for _, p := range posts {
		if p.Photo != nil {
			byLeg[p.LegID] = append(byLeg[p.LegID], p)
			total++
		}
	}
	var groups []legPhotos
	for _, l := range rv.Legs {
		if ps := byLeg[l.ID]; len(ps) > 0 {
			groups = append(groups, legPhotos{Leg: l, Posts: ps})
		}
	}
	// The gallery spans the ride, so it wears the ride's overall look: leg 1.
	pageLeg := ""
	if len(rv.Legs) > 0 {
		pageLeg = rv.Legs[0].Slug
	}
	a.render(w, r, "photos.html", map[string]any{
		"Title": "Photos", "Ride": rv.Ride, "Groups": groups, "Total": total, "PageLeg": pageLeg})
}

// routeGeoJSON is the ride as open data: legs as LineStrings, checkpoints
// and pinned posts as Points. Fuzzed checkpoints are already snapped by the
// store, and a post pinned to one carries the snapped point.
func (a *App) routeGeoJSON(w http.ResponseWriter, r *http.Request) {
	rv, err := a.loadRide()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	posts, err := a.store.Posts(rv.Ride.Slug, 0)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	type feature struct {
		Type     string         `json:"type"`
		Geometry map[string]any `json:"geometry"`
		Props    map[string]any `json:"properties"`
	}
	feats := []feature{}
	for _, l := range rv.Legs {
		feats = append(feats, feature{Type: "Feature",
			Geometry: map[string]any{"type": "LineString", "coordinates": l.Line},
			Props: map[string]any{"kind": "leg", "leg": l.Ord, "slug": l.Slug, "name": l.Name,
				"date": l.Date, "est_miles": l.EstMiles, "est_drive_hours": l.EstHours,
				"route_hint": l.RouteHint}})
	}
	// The actual track, one LineString per leg that has one. It is a separate
	// feature rather than a replacement for the planned leg: both are true, and
	// the difference between them is the interesting part.
	tracks, planned := driven(rv.Legs, posts)
	for _, l := range rv.Legs {
		pts, ok := tracks[l.ID]
		if !ok {
			continue
		}
		props := map[string]any{"kind": "track", "leg": l.Ord, "slug": l.Slug, "name": l.Name,
			"date": l.Date, "points": len(pts)}
		// Say which vertices are evidence: with planned_start the first one is
		// the leg's planned origin, and the evidence begins at the second.
		if planned[l.ID] {
			props["planned_start"] = true
		}
		feats = append(feats, feature{Type: "Feature",
			Geometry: map[string]any{"type": "LineString", "coordinates": pts},
			Props:    props})
	}
	for _, l := range rv.Legs {
		for _, c := range l.Checkpoints {
			// id is here so curl can address the checkpoint it just read — the
			// arrive route is keyed by id, and ord is not it.
			p := map[string]any{"kind": "checkpoint", "id": c.ID, "order": c.Ord, "name": c.Name,
				"label": c.Label, "leg": l.Ord, "tz": c.TZ, "precision": c.Precision,
				"is_terminal": c.IsTerminal, "mark": c.Mark}
			if c.PlannedArrive != "" {
				p["planned_arrive"] = c.PlannedArrive
			}
			if c.PlannedDepart != "" {
				p["planned_depart"] = c.PlannedDepart
			}
			if c.ArrivedAt != "" {
				p["arrived_at"] = c.ArrivedAt
				p["made_it"] = true
			}
			feats = append(feats, feature{Type: "Feature",
				Geometry: map[string]any{"type": "Point", "coordinates": []float64{c.Lng, c.Lat}},
				Props:    p})
		}
	}
	for _, p := range posts {
		if p.PinLat == nil {
			continue
		}
		props := map[string]any{"kind": "post", "id": p.ID, "post_kind": p.Kind, "title": p.Title,
			"occurred_at": p.OccurredAt, "tz": p.TZ, "leg": p.LegOrd, "pin_source": p.PinSource}
		if p.PinSource == "checkpoint" && p.CheckpointName != "" {
			props["near"] = p.CheckpointName
		}
		if p.AccuracyM != nil {
			props["accuracy_m"] = *p.AccuracyM
		}
		if p.Photo != nil {
			props["photo"] = "/media/" + p.Photo.Path
			props["thumb"] = "/media/" + p.Photo.ThumbPath
		}
		feats = append(feats, feature{Type: "Feature",
			Geometry: map[string]any{"type": "Point", "coordinates": []float64{*p.PinLng, *p.PinLat}},
			Props:    props})
	}
	out := map[string]any{
		"type": "FeatureCollection",
		"properties": map[string]any{
			"ride": rv.Ride.Slug, "name": rv.Ride.Name, "mode": rv.Ride.Mode, "topology": "ONE-WAY",
			"start_date": rv.Ride.StartDate, "end_date": rv.Ride.EndDate,
			"origin": rv.Ride.OriginLabel, "terminus": rv.Ride.TerminusLabel,
			"note": "Planning-grade coordinates (town centroids, ~1 km). Times are UTC; " +
				"each checkpoint carries its own IANA tz. Fuzzed points are snapped to a 0.05° grid. " +
				"kind=post points are pinned posts; pin_source says whether the point is the photo's " +
				"EXIF GPS, the phone's browser fix (accuracy_m), or the nearest checkpoint (near). " +
				"kind=leg is the plan, and its ends follow any checkpoint stamped made_it; kind=track " +
				"is what the ride is known to have actually driven — arrived origin, pinned posts in " +
				"time order, arrived terminus — and exists only for legs that have some of it. A track " +
				"with planned_start begins at the leg's planned origin, which was not stamped: its " +
				"first vertex is the plan and the rest is evidence.",
		},
		"features": feats,
	}
	w.Header().Set("Content-Type", "application/geo+json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=300")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		log.Printf("ERROR geojson encode: %v", err)
	}
}

// --- admin -----------------------------------------------------------------------

func (a *App) loginForm(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Title": "Sign in", "Next": r.URL.Query().Get("next")}
	if !a.auth.enabled() {
		data["Error"] = "Posting is disabled: no users exist on the server."
	}
	a.render(w, r, "login.html", data)
}

func (a *App) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	username := strings.ToLower(strings.TrimSpace(r.PostFormValue("username")))
	if !a.auth.Check(username, r.PostFormValue("password")) {
		w.WriteHeader(http.StatusUnauthorized)
		a.render(w, r, "login.html", map[string]any{"Title": "Sign in", "Error": "Incorrect username or password.",
			"Next": r.PostFormValue("next")})
		return
	}
	a.auth.Issue(w, a.secure, username)
	next := r.PostFormValue("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/driver"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	a.auth.Clear(w)
	http.Redirect(w, r, "/", http.StatusFound)
}

// postForm is the one thumb-sized screen: photo, kind, title, body, leg,
// checkpoint, zone, hide-location. Leg defaults to today's, zone follows the
// last arrival, and picking a checkpoint re-selects its zone (see the inline
// script in the template).
func (a *App) postForm(w http.ResponseWriter, r *http.Request) {
	rv, err := a.loadRide()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	di := dayOf(rv.Ride, rv.Legs, time.Now())
	dl := defaultLeg(rv.Legs, di)
	var dlID int64
	if dl != nil {
		dlID = dl.ID
	}
	tz := defaultTZ(rv.Legs, dl)
	// The private overlay is re-read here, on the admin page only.
	pd, err := loadPrivate(a.dataDir)
	if err != nil {
		log.Printf("WARN private overlay: %v", err)
	}
	data := map[string]any{
		"Title": "Post", "Ride": rv.Ride, "Legs": rv.Legs, "Kinds": postKinds,
		// This ride's addresses and no other's: an ord means nothing without its ride.
		"Private":      pd.forRide(rv.Ride.Slug, a.rideSlug).addressByOrd(),
		"DefaultLegID": dlID, "DefaultLeg": dl, "DefaultTZ": tz, "Zones": zones(rv.Legs, tz),
		"Day": di, "Error": r.URL.Query().Get("error"),
		// What the form needs to say, before posting, what will be published:
		// the private cells the fuzz toggle pre-ticks itself for, and which way
		// PIN_PRECISION tips that default. These are the ALREADY-FUZZED
		// coordinates the public GeoJSON carries, not the real points.
		"PrivateCells": privateCells(rv.Legs), "PinPrecision": a.pinPrecision,
	}
	if dl != nil {
		data["PageLeg"] = dl.Slug
	}
	a.render(w, r, "post_form.html", data)
}

// --- the pin ladder -------------------------------------------------------------
//
// A post's pin has three possible sources and one privacy control. Both the
// post form and the re-pin control on an existing post run through resolvePin
// so the two cannot drift apart.
//
// Kris ruled the shape on 2026-09-02: "protect personal locations, be precise
// with public locations", and "fuzz location is a good toggle". So the point
// is stored exactly by default and coarsened only when the operator says so —
// with the toggle pre-ticked wherever the point falls at a private place, so
// leaving the form alone still protects the home.

const (
	pinEXIF       = "exif"
	pinBrowser    = "browser"
	pinCheckpoint = "checkpoint"
	pinNone       = "none"
)

// validPinSource guards the form's pin_source radio. "" on the wire means
// "nobody chose — run the ladder", which is also what a form served by a
// build from before the radio existed submits.
func validPinSource(v string) bool {
	switch v {
	case pinEXIF, pinBrowser, pinCheckpoint, pinNone:
		return true
	}
	return false
}

// pinInput is everything one post's pin can be resolved from.
type pinInput struct {
	// Want is the operator's explicit choice (pin_source). "" runs the ladder
	// exif > browser > checkpoint, which is also what the form pre-selects, so
	// a post where nothing is touched behaves exactly as it did before the
	// radio existed.
	Want string
	// ExifLat/Lng are read by the SERVER out of the uploaded file (photo.go),
	// never from the form. The form does post photo_lat/photo_lng, but those
	// are the browser's own parse, for its display line only — a client could
	// say anything and the file is the fact.
	ExifLat, ExifLng *float64
	BrowLat, BrowLng *float64
	Accuracy         *float64
	CP               *Checkpoint
	// Fuzz is the "Fuzz location" toggle; FuzzSet says the operator's page
	// actually carried it (the form posts a companion fuzz_choice field,
	// because an unticked checkbox sends nothing). Unset = the per-post
	// default: ticked at a private place, clear everywhere else.
	Fuzz, FuzzSet bool
}

// pinResult is what the store gets, plus the one log line.
type pinResult struct {
	Lat, Lng     *float64
	Source       string
	AccuracyM    *float64
	CheckpointID int64 // set when fuzzing moved the post into a checkpoint's cell
	Fuzzed       bool
	Note         string
}

// resolvePin runs the ladder (or honours an explicit choice) and then applies
// the fuzz toggle. precision is PIN_PRECISION, and it decides ONE thing: the
// toggle's default state at a private place — "snap" (the default: fuzz a
// point that falls in a fuzzed checkpoint's cell) or "exact" (publish it as
// taken). Everywhere else the default is exact in both modes, because a point
// that is not at a personal place needs no protection.
func resolvePin(in pinInput, cps []Checkpoint, precision string) pinResult {
	res := pinResult{Source: pinNone, Note: "none (no fix, no checkpoint)"}

	have := func(src string) (lat, lng, acc *float64, ok bool) {
		switch src {
		case pinEXIF:
			if in.ExifLat != nil && in.ExifLng != nil {
				return in.ExifLat, in.ExifLng, nil, true
			}
		case pinBrowser:
			if in.BrowLat != nil && in.BrowLng != nil {
				return in.BrowLat, in.BrowLng, in.Accuracy, true
			}
		case pinCheckpoint:
			if in.CP != nil {
				lat, lng := in.CP.Lat, in.CP.Lng
				return &lat, &lng, nil, true
			}
		}
		return nil, nil, nil, false
	}

	chosen, fellBack := "", ""
	switch {
	case in.Want == pinNone:
		res.Note = "none (chosen)"
		return res
	case in.Want != "":
		if _, _, _, ok := have(in.Want); ok {
			chosen = in.Want
		} else {
			fellBack = in.Want
		}
	}
	if chosen == "" {
		for _, src := range []string{pinEXIF, pinBrowser, pinCheckpoint} {
			if _, _, _, ok := have(src); ok {
				chosen = src
				break
			}
		}
	}
	if chosen == "" {
		if fellBack != "" {
			res.Note = fellBack + " chosen, but that source has no coordinates → none"
		}
		return res
	}
	lat, lng, acc, _ := have(chosen)
	res.Lat, res.Lng, res.Source, res.AccuracyM = lat, lng, chosen, acc
	if in.CP != nil {
		res.CheckpointID = in.CP.ID
	}
	switch chosen {
	case pinEXIF:
		res.Note = "exif"
	case pinBrowser:
		res.Note = "browser (no accuracy)"
		if acc != nil {
			res.Note = fmt.Sprintf("browser ±%.0f m", *acc)
		}
	case pinCheckpoint:
		res.Note = fmt.Sprintf("checkpoint %d (%s)", in.CP.Ord, in.CP.Name)
	}
	switch {
	case fellBack != "":
		res.Note = fellBack + " chosen, but that source has no coordinates → " + res.Note
	case in.Want != "":
		res.Note += " (chosen)"
	case chosen == pinCheckpoint:
		res.Note += " (fallback)"
	}

	// A checkpoint pin is the checkpoint's own point, which the store has
	// already snapped if the checkpoint is private. Nothing left to fuzz.
	if res.Source != pinEXIF && res.Source != pinBrowser {
		return res
	}

	// The privacy seam. nearPrivate is the "personal place" test: the point
	// sits in the same 0.05° cell as a precision=fuzzed checkpoint, or within
	// 2 km of it. It is the ONLY thing that pre-ticks the toggle.
	pc := nearPrivate(*res.Lat, *res.Lng, cps)
	fuzz := pc != nil && precision != pinPrecisionExact
	if in.FuzzSet {
		fuzz = in.Fuzz
	}
	where := "here"
	if pc != nil {
		where = fmt.Sprintf("at checkpoint %d (%s)", pc.Ord, pc.Name)
	}
	if !fuzz {
		if pc != nil {
			res.Note += fmt.Sprintf(" (exact, fuzz off) %s", where)
		}
		return res
	}
	res.Fuzzed, res.AccuracyM = true, nil
	if pc != nil {
		// Name the place it belongs to, so the popup can say "near <name>" —
		// and move the post onto that checkpoint, as the pre-toggle build did.
		plat, plng := pc.Lat, pc.Lng
		res.Lat, res.Lng, res.Source = &plat, &plng, pinCheckpoint
		res.CheckpointID = pc.ID
		res.Note += fmt.Sprintf(" → fuzzed to checkpoint %d (%s)", pc.Ord, pc.Name)
		if precision == pinPrecisionExact {
			res.Note += " (fuzz on)"
		}
		return res
	}
	// Somewhere with no checkpoint of its own — a friend's driveway. Coarsen
	// the point to the grid and keep the source; there is no name to carry.
	flat, flng := snap(*res.Lat), snap(*res.Lng)
	res.Lat, res.Lng = &flat, &flng
	res.Note += " → fuzzed to the 0.05° grid (no checkpoint)"
	return res
}

// apply copies a resolved pin onto a post.
func (res pinResult) apply(p *Post) {
	p.Lat, p.Lng, p.PinSource, p.AccuracyM, p.PinFuzzed = res.Lat, res.Lng, res.Source, res.AccuracyM, res.Fuzzed
	if res.CheckpointID != 0 {
		p.CheckpointID = res.CheckpointID
	}
}

// pinInputFrom reads the pin half of a submitted form. exifLat/exifLng are
// the SERVER's own read of the photo, passed in by the caller.
func pinInputFrom(r *http.Request, cp *Checkpoint, exifLat, exifLng *float64) pinInput {
	in := pinInput{
		Want:    r.PostFormValue("pin_source"),
		ExifLat: exifLat, ExifLng: exifLng,
		BrowLat: formCoord(r, "lat", 90),
		BrowLng: formCoord(r, "lng", 180),
		CP:      cp,
		Fuzz:    r.PostFormValue("fuzz_location") != "",
		FuzzSet: r.PostFormValue("fuzz_choice") != "",
	}
	if !validPinSource(in.Want) {
		in.Want = ""
	}
	if acc, err := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue("accuracy")), 64); err == nil && acc >= 0 && acc < 1e6 {
		acc = math.Round(acc)
		in.Accuracy = &acc
	}
	return in
}

func (a *App) postCreate(w http.ResponseWriter, r *http.Request) {
	rv, err := a.loadRide()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fail := func(msg string) {
		http.Redirect(w, r, "/driver/post?error="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	// Slack above the photo cap for the text fields and multipart framing.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(1<<20))
	// The photo part spills to disk past 1 MB — into DATA_DIR/tmp, which
	// main() creates and points TMPDIR at, since scratch has no /tmp. Holding
	// a 20 MB upload here, then again in savePhoto, plus the decode, is what
	// OOM-killed the 256 MB VM on 2026-09-02.
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			fail(fmt.Sprintf("That upload is over the %d MB limit.", maxUploadBytes>>20))
			return
		}
		fail("Could not read the form: " + err.Error())
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	legID, _ := strconv.ParseInt(r.PostFormValue("leg_id"), 10, 64)
	var leg *Leg
	for i := range rv.Legs {
		if rv.Legs[i].ID == legID {
			leg = &rv.Legs[i]
		}
	}
	var cp *Checkpoint
	if v := r.PostFormValue("checkpoint_id"); v != "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		for i := range rv.Legs {
			for j := range rv.Legs[i].Checkpoints {
				if rv.Legs[i].Checkpoints[j].ID == id {
					cp = &rv.Legs[i].Checkpoints[j]
					leg = &rv.Legs[i] // the checkpoint is the more specific fact
				}
			}
		}
	}
	if leg == nil {
		fail("Pick a leg.")
		return
	}

	tz := strings.TrimSpace(r.PostFormValue("tz"))
	if _, err := time.LoadLocation(tz); err != nil || tz == "" {
		if cp != nil {
			tz = cp.TZ
		} else {
			tz = leg.TZ
		}
	}
	loc := loadLoc(tz)

	kind := r.PostFormValue("kind")
	if !validKind(kind) {
		kind = "note"
	}
	// The pin-source radio's "No pin" and the old hide_location checkbox mean
	// the same thing and both are accepted: a form submitted by a page from
	// before the radio existed still hides its location.
	post := &Post{LegID: leg.ID, Kind: kind, TZ: tz,
		Title:        strings.TrimSpace(r.PostFormValue("title")),
		Body:         strings.TrimSpace(strings.ReplaceAll(r.PostFormValue("body"), "\r\n", "\n")),
		HideLocation: r.PostFormValue("hide_location") != "" || r.PostFormValue("pin_source") == pinNone}
	if cp != nil {
		post.CheckpointID = cp.ID
	}

	var photo *Photo
	if f, _, err := r.FormFile("photo"); err == nil {
		defer f.Close()
		photo, err = a.savePhoto(f, loc)
		if err != nil {
			fail(err.Error())
			return
		}
		post.PhotoID = photo.ID
		if post.Kind == "note" {
			post.Kind = "photo"
		}
	} else if !errors.Is(err, http.ErrMissingFile) {
		fail("Could not read the photo: " + err.Error())
		return
	}

	if post.Title == "" && post.Body == "" && photo == nil {
		fail("Say something, or attach a photo.")
		return
	}

	// Pin: whatever the operator picked on the radio, else the ladder — the
	// photo's EXIF GPS, else the browser fix the form attached, else the
	// chosen checkpoint. Safari strips everything but orientation from a web
	// camera capture, so on the road the browser fix is the usual winner.
	// Hiding the location beats all of it and stores nothing.
	//
	// pinNote is the one server log line per post that says what was decided —
	// what a driveway GPS test proves from `flyctl logs`, since the public
	// surface hides a home fix by default. Names, never coordinates.
	post.PinSource = pinNone
	pinNote := "none (hidden)"
	if !post.HideLocation {
		// The EXIF point is the one the SERVER read out of the uploaded file.
		// photo_lat/photo_lng came from the browser's own parse and are never
		// trusted here — they exist so the form could show the operator what
		// it found before posting.
		var exLat, exLng *float64
		if photo != nil {
			exLat, exLng = photo.Lat, photo.Lng
		}
		res := resolvePin(pinInputFrom(r, cp, exLat, exLng), rideCheckpoints(rv.Legs), a.pinPrecision)
		res.apply(post)
		pinNote = res.Note
	}

	// When: the form's explicit time (local to the chosen zone), else the
	// photo's EXIF time, else now.
	switch {
	case strings.TrimSpace(r.PostFormValue("when")) != "":
		t, err := time.ParseInLocation("2006-01-02T15:04", strings.TrimSpace(r.PostFormValue("when")), loc)
		if err != nil {
			fail("Could not read the time; use the picker.")
			return
		}
		post.OccurredAt = utcString(t)
	case photo != nil && photo.TakenAt != "":
		post.OccurredAt = photo.TakenAt
	default:
		post.OccurredAt = utcString(time.Now())
	}

	if err := a.store.CreatePost(post); err != nil {
		fail("Could not save: " + err.Error())
		return
	}
	log.Printf("post %d: pin %s", post.ID, pinNote)
	http.Redirect(w, r, "/#post-"+strconv.FormatInt(post.ID, 10), http.StatusSeeOther)
}

// formCoord reads a decimal coordinate field, rounded to 4 dp (≈11 m, all a
// phone fix is worth); nil when absent, unparsable or out of range.
func formCoord(r *http.Request, name string, limit float64) *float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue(name)), 64)
	if err != nil || math.IsNaN(v) || v < -limit || v > limit {
		return nil
	}
	v = math.Round(v*1e4) / 1e4
	return &v
}

// checkpointArrive is the one-tap stamp and, with a location, the "made it to"
// record: the moment the plan's town centroid is replaced by the place the car
// actually stopped. Form field clear=1 un-stamps a mis-tap and leaves the
// location alone, because the stamp and the place are separate facts.
//
// A location comes one of two ways:
//
//   - post_id — adopt that post's PUBLISHED pin. The lodging photo IS the fix,
//     and its pin has already been through the fuzz seam, so nothing raw can
//     enter here by this door.
//   - lat + lng — an explicit point, for a stop nobody photographed.
//
// Neither can move a fuzzed checkpoint off its cell: the store snaps it before
// writing (SetArrived), so a private place gets a time stamp and keeps its
// grid point.
//
// Field at=<RFC3339> stamps a time other than now. The button never sends it —
// a tap means "now", and that is the whole point of a one-tap stamp. It exists
// for a repair: a checkpoint the seed has stopped owning (arrived_at is what
// takes ownership away) can only be corrected by clearing the stamp, letting a
// deploy refresh it, and stamping it again — and without this the third step
// would overwrite the real arrival with the clock of the repair. It came up on
// 2026-09-07, when checkpoints 4 and 5 were found still carrying the names
// they had before two seed corrections.
func (a *App) checkpointArrive(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cp, err := a.store.Checkpoint(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()

	clearing := r.PostFormValue("clear") != ""
	at := time.Now()
	if v := strings.TrimSpace(r.PostFormValue("at")); v != "" {
		if clearing {
			http.Error(w, "at and clear contradict each other: one stamps a time, the other removes it",
				http.StatusBadRequest)
			return
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "at must be RFC3339 with an offset, e.g. 2026-09-06T20:06:47-07:00",
				http.StatusBadRequest)
			return
		}
		// The zero time is how the store spells "no stamp" (SetArrived), so a
		// literal 0001-01-01 would clear the checkpoint instead of stamping it.
		if t.IsZero() {
			http.Error(w, "at is the zero time, which is how a cleared stamp is spelled", http.StatusBadRequest)
			return
		}
		// You cannot have arrived somewhere you have not reached yet, and a
		// future stamp goes wrong quietly: defaultTZ follows the LATEST
		// arrival, so a mistyped year would own the post form's zone for the
		// rest of the ride. The tolerance is for clock skew between whatever
		// sent this and the machine.
		if t.After(time.Now().Add(2 * time.Minute)) {
			http.Error(w, "at is in the future", http.StatusBadRequest)
			return
		}
		at = t
	}
	if clearing {
		at = time.Time{}
	}

	var loc *LatLng
	switch pid := strings.TrimSpace(r.PostFormValue("post_id")); {
	case clearing:
		// Clearing is about the stamp alone.
	case pid != "":
		n, err := strconv.ParseInt(pid, 10, 64)
		if err != nil {
			http.Error(w, "post_id is not a post id", http.StatusBadRequest)
			return
		}
		p, err := a.store.Post(n)
		if err != nil {
			http.Error(w, "no post "+pid, http.StatusNotFound)
			return
		}
		if p.PinLat == nil {
			http.Error(w, "post "+pid+" has no published pin to adopt", http.StatusBadRequest)
			return
		}
		loc = &LatLng{Lat: *p.PinLat, Lng: *p.PinLng}
	default:
		lat, lng := formCoord(r, "lat", 90), formCoord(r, "lng", 180)
		if (lat == nil) != (lng == nil) {
			http.Error(w, "lat and lng travel together", http.StatusBadRequest)
			return
		}
		if lat != nil {
			loc = &LatLng{Lat: *lat, Lng: *lng}
		}
	}

	if err := a.store.SetArrived(id, at, loc); err != nil {
		http.Error(w, "could not stamp", 500)
		return
	}
	// Names and the privacy outcome, never the point — the same rule the pin
	// log line follows, and this one can carry a lodging fix. A back-dated
	// stamp says so and says when: an arrival time is already public in
	// /route.geojson, and a repair that quietly rewrote one would be worse
	// unsaid than said.
	when := ""
	if !clearing && !at.Equal(time.Time{}) && r.PostFormValue("at") != "" {
		when = " (at " + utcString(at) + ", supplied)"
	}
	switch {
	case clearing:
		log.Printf("checkpoint %d (%s): arrival cleared; the location stays", cp.Ord, cp.Name)
	case loc == nil:
		log.Printf("checkpoint %d (%s): arrived%s", cp.Ord, cp.Name, when)
	case cp.Precision == precisionFuzzed:
		log.Printf("checkpoint %d (%s): arrived%s, location adopted and snapped (private place)", cp.Ord, cp.Name, when)
	default:
		log.Printf("checkpoint %d (%s): arrived%s, location adopted → precision exact", cp.Ord, cp.Name, when)
	}
	http.Redirect(w, r, backTo(r, "/driver/post"), http.StatusSeeOther)
}

// --- the census ------------------------------------------------------------------
//
// /route.geojson is the ride as open data and skips a post with no pin. That
// makes it the wrong instrument for the one question an operator has after a
// week of posting from a moving car: what is actually in there, and did the
// hotspot post that photo twice? The census answers it — EVERY post, pinned or
// not, hidden or not, with the photo's identity attached, because that is what
// finds a duplicate: the same photo.id or sha256 twice, or two rows whose
// taken_at is seconds apart.
//
// It is behind requireAuth like every other /driver route, so with the push
// token it is one curl away and with neither credential it does not exist.

type censusPhoto struct {
	ID      int64  `json:"id"`
	Path    string `json:"path"`
	Thumb   string `json:"thumb"`
	SHA256  string `json:"sha256"`
	TakenAt string `json:"taken_at"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}

type censusPost struct {
	ID           int64  `json:"id"`
	Leg          int    `json:"leg"`
	CheckpointID int64  `json:"checkpoint_id"`
	Checkpoint   string `json:"checkpoint"`
	Kind         string `json:"kind"`
	OccurredAt   string `json:"occurred_at"`
	TZ           string `json:"tz"`
	Title        string `json:"title"`
	CreatedAt    string `json:"created_at"`
	PinSource    string `json:"pin_source"`
	PinFuzzed    bool   `json:"pin_fuzzed"`
	// The PUBLISHED point, which is null for a hidden or unpinned post — the
	// same value the map and the GeoJSON carry, never the raw fix.
	Lat   *float64     `json:"lat"`
	Lng   *float64     `json:"lng"`
	Photo *censusPhoto `json:"photo"`
}

func (a *App) postsJSON(w http.ResponseWriter, r *http.Request) {
	rv, err := a.loadRide()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	posts, err := a.store.Posts(rv.Ride.Slug, 0)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	out := []censusPost{}
	for _, p := range posts {
		c := censusPost{ID: p.ID, Leg: p.LegOrd, CheckpointID: p.CheckpointID, Checkpoint: p.CheckpointName,
			Kind: p.Kind, OccurredAt: p.OccurredAt, TZ: p.TZ, Title: p.Title, CreatedAt: p.CreatedAt,
			PinSource: p.PinSource, PinFuzzed: p.PinFuzzed, Lat: p.PinLat, Lng: p.PinLng}
		if p.Photo != nil {
			c.Photo = &censusPhoto{ID: p.Photo.ID, Path: "/media/" + p.Photo.Path,
				Thumb: "/media/" + p.Photo.ThumbPath, SHA256: p.Photo.SHA256,
				TakenAt: p.Photo.TakenAt, Width: p.Photo.Width, Height: p.Photo.Height}
		}
		out = append(out, c)
	}
	w.Header().Set("Content-Type", "application/json")
	// Signed-in only and never shared: no cache, and no CORS header either.
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"ride": rv.Ride.Slug, "count": len(out), "posts": out}); err != nil {
		log.Printf("ERROR posts.json encode: %v", err)
	}
}

func (a *App) postDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := a.store.Post(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := a.store.DeletePost(p.ID); err != nil {
		http.Error(w, "could not delete", 500)
		return
	}
	// The photo file stays on the volume: content-addressed, and a second
	// post may reference it. Nothing on this site deletes bytes.
	http.Redirect(w, r, backTo(r, "/"), http.StatusSeeOther)
}

// postRepin re-chooses an existing post's pin from what that post still has:
// its photo's EXIF GPS, its checkpoint, or no pin at all — plus the same fuzz
// toggle. A browser fix is not re-derivable after the fact and is therefore
// not offered; a post that has one keeps it by choosing nothing.
func (a *App) postRepin(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	post, err := a.store.Post(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rv, err := a.loadRide()
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	_ = r.ParseForm()

	var cp *Checkpoint
	if post.CheckpointID != 0 {
		for i := range rv.Legs {
			for j := range rv.Legs[i].Checkpoints {
				if rv.Legs[i].Checkpoints[j].ID == post.CheckpointID {
					cp = &rv.Legs[i].Checkpoints[j]
				}
			}
		}
	}
	var exLat, exLng *float64
	if post.Photo != nil {
		exLat, exLng = post.Photo.Lat, post.Photo.Lng
	}
	in := pinInputFrom(r, cp, exLat, exLng)
	// The stored browser fix, if this post has one, so "leave it alone" is a
	// real option: re-pinning must not silently drop a pin it cannot re-derive.
	if post.PinSource == pinBrowser && post.Lat != nil {
		in.BrowLat, in.BrowLng, in.Accuracy = post.Lat, post.Lng, post.AccuracyM
	}
	post.HideLocation = in.Want == pinNone
	if post.HideLocation {
		post.Lat, post.Lng, post.PinSource, post.AccuracyM, post.PinFuzzed = nil, nil, pinNone, nil, false
		post.CheckpointID = 0
		log.Printf("post %d: re-pinned → none (hidden)", post.ID)
	} else {
		res := resolvePin(in, rideCheckpoints(rv.Legs), a.pinPrecision)
		post.Lat, post.Lng, post.PinSource, post.AccuracyM, post.PinFuzzed = nil, nil, pinNone, nil, false
		res.apply(post)
		log.Printf("post %d: re-pinned → pin %s", post.ID, res.Note)
	}
	if err := a.store.UpdatePostPin(post.ID, post); err != nil {
		http.Error(w, "could not re-pin", 500)
		return
	}
	http.Redirect(w, r, backTo(r, "/")+"#post-"+strconv.FormatInt(post.ID, 10), http.StatusSeeOther)
}

// maxPrivateBytes caps the carriage's body. The overlay is a handful of
// addresses; anything approaching this is a mistake or an attack, and the
// whole document is held in memory to be parsed on a 512 MB machine.
const maxPrivateBytes = 64 << 10 // 64 KB

// lodgingForm is the private overlay's edit screen — the addresses and notes
// for each checkpoint, which never enter the repository or the seed and live
// only in $DATA_DIR/private.json on the volume.
//
// It exists so the overlay can be corrected from the road, from the phone,
// the way a post can. Before it, the only way in was an sftp prompt from a
// laptop, which is not a thing anyone has in a moving car.
func (a *App) lodgingForm(w http.ResponseWriter, r *http.Request) {
	rv, err := a.loadRide()
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	pd, err := loadPrivate(a.dataDir)
	if err != nil {
		log.Printf("WARN private overlay: %v", err)
	}
	a.render(w, r, "lodging.html", map[string]any{
		"Title": "Lodging", "Ride": rv.Ride, "Legs": rv.Legs,
		"Private": pd.forRide(rv.Ride.Slug, a.rideSlug).byOrd(),
		"Error":   r.URL.Query().Get("error"),
		"Saved":   r.URL.Query().Get("saved") != "",
	})
}

// lodgingSave rewrites the ACTIVE ride's block of the overlay from the form,
// and no other ride's. The form carries every checkpoint of that ride, so the
// block it produces is complete: an entry left blank is an entry deleted,
// which is how a stale address gets removed.
func (a *App) lodgingSave(w http.ResponseWriter, r *http.Request) {
	rv, err := a.loadRide()
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	fail := func(msg string) {
		http.Redirect(w, r, "/driver/lodging?error="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPrivateBytes)
	if err := r.ParseForm(); err != nil {
		fail("Could not read the form.")
		return
	}
	// The other rides' blocks ride along untouched, so the file has to be read
	// first — and a file that will not read is not one to overwrite.
	pd, err := loadPrivate(a.dataDir)
	if err != nil {
		log.Printf("WARN private overlay: %v", err)
		fail("Could not read the overlay that is on the volume, so nothing was saved.")
		return
	}
	block := map[string]privateCheckpoint{}
	for _, l := range rv.Legs {
		for _, c := range l.Checkpoints {
			key := strconv.Itoa(c.Ord)
			e := privateCheckpoint{
				Address: strings.TrimSpace(r.PostFormValue("address-" + key)),
				Notes:   strings.TrimSpace(r.PostFormValue("notes-" + key)),
			}
			if e.Address == "" && e.Notes == "" {
				continue // an empty entry is an absent entry
			}
			block[key] = e
		}
	}
	pd.setRide(rv.Ride.Slug, a.rideSlug, block)
	if err := savePrivate(a.dataDir, pd); err != nil {
		fail("Could not save: " + err.Error())
		return
	}
	// The count, never the content — this log ships to flyctl logs, and the
	// whole point of the overlay is that the addresses stay out of surfaces
	// like it.
	log.Printf("lodging: overlay saved for the active ride, %d checkpoint(s) carry detail", len(block))
	http.Redirect(w, r, "/driver/lodging?saved=1", http.StatusSeeOther)
}

// privatePush is the data carriage's landing point: the overlay as one JSON
// document.
//
// It exists because the overlay is the one piece of volume data that is NOT
// authored on prod. Posts, photos and arrival stamps are written from the road
// and prod is their source of record — pushing those up would destroy them.
// The overlay is operator-authored config with a canonical copy elsewhere (a
// repository secret), so it is the one thing that can safely travel upward,
// and the one thing that has to, because a fresh volume comes up without it.
//
// The overlay is keyed by ride, and what a push replaces follows from what it
// sends:
//
//	{"checkpoints": {…}}                 the FIRST ride's block (RIDE_SLUG),
//	                                     and only that: the shape the carriage
//	                                     has always sent, meaning what it
//	                                     always meant
//	{"checkpoints": {…}}  with ?ride=x   ride x's block, and only that
//	{"rides": {…}, "checkpoints": {…}}   the whole document
//
// So one ride's push never removes another ride's entries.
func (a *App) privatePush(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPrivateBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "could not read the body (limit "+strconv.Itoa(maxPrivateBytes>>10)+" KB)", 400)
		return
	}
	var in privateData
	if err := json.Unmarshal(raw, &in); err != nil {
		http.Error(w, "not valid JSON: "+err.Error(), 400)
		return
	}
	// Keys are checkpoint ords. byOrd drops anything that is not a number, so
	// a typo'd key would be accepted here and then silently do nothing —
	// refuse it instead, while the pusher is still watching.
	checkOrds := func(cps map[string]privateCheckpoint) bool {
		for k := range cps {
			if _, err := strconv.Atoi(k); err != nil {
				http.Error(w, "checkpoint key "+strconv.Quote(k)+" is not a checkpoint ord", 400)
				return false
			}
		}
		return true
	}
	if !checkOrds(in.Checkpoints) {
		return
	}
	for slug, block := range in.Rides {
		if !validSlug(slug) {
			http.Error(w, "a key under rides is not a ride slug", 400)
			return
		}
		if slug == a.rideSlug && len(in.Checkpoints) > 0 {
			http.Error(w, "the first ride has two blocks: the top-level checkpoints and one under rides; send one", 400)
			return
		}
		if !checkOrds(block.Checkpoints) {
			return
		}
	}
	ride := strings.TrimSpace(r.URL.Query().Get("ride"))
	var out privateData
	switch {
	case ride != "":
		if in.Rides != nil {
			http.Error(w, "?ride= takes one ride's block, {\"checkpoints\": {…}}, not a document with rides", 400)
			return
		}
		if !validSlug(ride) {
			http.Error(w, "ride is not a ride slug", 400)
			return
		}
		if _, err := a.store.Ride(ride); err != nil {
			http.Error(w, "no ride "+strconv.Quote(ride)+" in the database", http.StatusNotFound)
			return
		}
		fallthrough
	case in.Rides == nil:
		// One ride's block. Everything else on the volume stays as it is, so
		// the file has to read cleanly first.
		if ride == "" {
			ride = a.rideSlug
		}
		out, err = loadPrivate(a.dataDir)
		if err != nil {
			log.Printf("private overlay: push refused, the overlay on the volume does not parse: %v", err)
			http.Error(w, "the overlay on the volume does not parse; push a whole document (with rides) to replace it", http.StatusConflict)
			return
		}
		out.setRide(ride, a.rideSlug, in.Checkpoints)
	default:
		out = privateData{}
		for slug, block := range in.Rides {
			out.setRide(slug, a.rideSlug, block.Checkpoints)
		}
		if len(in.Checkpoints) > 0 {
			out.setRide(a.rideSlug, a.rideSlug, in.Checkpoints)
		}
	}
	if err := savePrivate(a.dataDir, out); err != nil {
		log.Printf("private overlay: push failed: %v", err)
		http.Error(w, "could not write the overlay", 500)
		return
	}
	// checkpoints is the count the carriage compares against what it sent: the
	// entries in the block it pushed, or in the whole document when it pushed
	// that.
	sent := len(in.Checkpoints)
	for _, block := range in.Rides {
		sent += len(block.Checkpoints)
	}
	log.Printf("private overlay: pushed, %d checkpoint(s) carry detail; the overlay now holds %d", sent, out.entries())
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, "{\"status\":\"ok\",\"checkpoints\":%d,\"total\":%d}\n", sent, out.entries())
}

// cpID is a checkpoint's id, or 0 for "no checkpoint" — the store's own
// spelling for the association being absent.
func cpID(cp *Checkpoint) int64 {
	if cp == nil {
		return 0
	}
	return cp.ID
}

// safeBack sanitises the edit form's carried return path. It is a same-site
// path or nothing: a value off the wire that could name another origin would
// turn a signed-in POST into an open redirect.
func safeBack(v, fallback string) string {
	if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") {
		return fallback
	}
	return path.Clean(v)
}

// postEditForm is the after-the-fact correction screen: the metadata a post
// can still be wrong about once it is up — its kind, its leg, its checkpoint,
// the zone and time it claims, and its words. A photo is content, not
// metadata, and is not editable here (delete and re-post); the pin has its own
// control on the post card, so that this screen can be used without touching
// a deliberate re-pin.
func (a *App) postEditForm(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	post, err := a.store.Post(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rv, err := a.loadRide()
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	// The private overlay is re-read here, on the admin page only — the same
	// rule the post form follows, so the checkpoint list reads alike on both.
	pd, err := loadPrivate(a.dataDir)
	if err != nil {
		log.Printf("WARN private overlay: %v", err)
	}
	data := map[string]any{
		"Title": "Edit post", "Ride": rv.Ride, "Legs": rv.Legs, "Kinds": postKinds,
		"Private": pd.forRide(rv.Ride.Slug, a.rideSlug).addressByOrd(), "Post": post,
		// The picker shows the stored instant read back in the post's OWN
		// zone, which is the wall clock the driver saw when they posted.
		"When": localFmt(post.OccurredAt, post.TZ, fmtInput),
		// The post's zone is included even if no checkpoint on the ride uses
		// it any more, or the select would silently re-file the post.
		"Zones": zones(rv.Legs, post.TZ),
		"Error": r.URL.Query().Get("error"),
		"Back":  backTo(r, "/"),
	}
	for i := range rv.Legs {
		if rv.Legs[i].ID == post.LegID {
			data["PageLeg"] = rv.Legs[i].Slug
		}
	}
	a.render(w, r, "post_edit.html", data)
}

// postEditSave writes the edit screen back.
func (a *App) postEditSave(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	post, err := a.store.Post(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rv, err := a.loadRide()
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	self := "/driver/post/" + strconv.FormatInt(id, 10) + "/edit"
	fail := func(msg string) {
		http.Redirect(w, r, self+"?error="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	if err := r.ParseForm(); err != nil {
		fail("Could not read the form.")
		return
	}

	// Leg and checkpoint resolve exactly as they do on the post form: a chosen
	// checkpoint is the more specific fact and carries its own leg with it, so
	// picking a checkpoint from another leg MOVES the post rather than leaving
	// the two contradicting each other.
	legID, _ := strconv.ParseInt(r.PostFormValue("leg_id"), 10, 64)
	var leg *Leg
	for i := range rv.Legs {
		if rv.Legs[i].ID == legID {
			leg = &rv.Legs[i]
		}
	}
	var cp *Checkpoint
	if v := r.PostFormValue("checkpoint_id"); v != "" {
		cid, _ := strconv.ParseInt(v, 10, 64)
		for i := range rv.Legs {
			for j := range rv.Legs[i].Checkpoints {
				if rv.Legs[i].Checkpoints[j].ID == cid {
					cp = &rv.Legs[i].Checkpoints[j]
					leg = &rv.Legs[i]
				}
			}
		}
	}
	if leg == nil {
		fail("Pick a leg.")
		return
	}

	// An unknown kind is refused rather than quietly reset to "note": on the
	// create form a bad kind means a stale page, but here it would silently
	// discard the kind the post already had.
	kind := r.PostFormValue("kind")
	if !validKind(kind) {
		fail("Pick a kind.")
		return
	}

	tz := strings.TrimSpace(r.PostFormValue("tz"))
	if _, err := time.LoadLocation(tz); err != nil || tz == "" {
		if cp != nil {
			tz = cp.TZ
		} else {
			tz = leg.TZ
		}
	}

	title := strings.TrimSpace(r.PostFormValue("title"))
	body := strings.TrimSpace(strings.ReplaceAll(r.PostFormValue("body"), "\r\n", "\n"))
	if title == "" && body == "" && post.PhotoID == 0 {
		fail("Say something — a post with no photo needs a title or a body.")
		return
	}

	// When: the picker's wall clock, read in whatever zone is now selected. So
	// correcting only the zone re-stamps the same wall clock into the new one,
	// which is what "that happened at 3 PM Mountain, not Pacific" means. An
	// empty picker leaves the stored instant alone.
	occurred := post.OccurredAt
	if v := strings.TrimSpace(r.PostFormValue("when")); v != "" {
		t, err := time.ParseInLocation(fmtInput, v, loadLoc(tz))
		if err != nil {
			fail("Could not read the time; use the picker.")
			return
		}
		occurred = utcString(t)
	}

	// A checkpoint-DERIVED pin has to follow its checkpoint, or the post names
	// one place and pins another. Every other source — the photo's own GPS, a
	// phone fix — is a fact about where the photo was taken, and an edit to
	// the post's filing must not touch it or its fuzz posture.
	movedCP := post.CheckpointID != cpID(cp)
	repin := movedCP && !post.HideLocation && post.PinSource == pinCheckpoint

	post.LegID, post.CheckpointID = leg.ID, cpID(cp)
	post.Kind, post.TZ = kind, tz
	post.Title, post.Body, post.OccurredAt = title, body, occurred

	if err := a.store.UpdatePostMeta(post.ID, post); err != nil {
		fail("Could not save: " + err.Error())
		return
	}
	log.Printf("post %d: edited → kind %s, leg %d (%s), checkpoint %d, zone %s",
		post.ID, post.Kind, leg.Ord, leg.Slug, post.CheckpointID, post.TZ)

	if repin {
		// Only the checkpoint can be the source here, so the ladder has
		// nothing to fall back to: clearing the checkpoint drops the pin, and
		// resolvePin's own note says exactly that. A checkpoint's point is
		// already snapped by the store when the checkpoint is private, so
		// there is no fuzz decision left to carry.
		post.Lat, post.Lng, post.PinSource, post.AccuracyM, post.PinFuzzed = nil, nil, pinNone, nil, false
		res := resolvePin(pinInput{Want: pinCheckpoint, CP: cp}, rideCheckpoints(rv.Legs), a.pinPrecision)
		res.apply(post)
		if err := a.store.UpdatePostPin(post.ID, post); err != nil {
			fail("Saved, but the pin did not move: " + err.Error())
			return
		}
		log.Printf("post %d: checkpoint pin followed the edit → pin %s", post.ID, res.Note)
	}

	back := safeBack(r.PostFormValue("back"), "/")
	http.Redirect(w, r, back+"#post-"+strconv.FormatInt(post.ID, 10), http.StatusSeeOther)
}

// privateCell is one fuzzed checkpoint as the post form's script sees it. The
// coordinates are the SNAPPED ones — the same values /route.geojson already
// publishes — so nothing private leaves the server here either.
type privateCell struct {
	Ord  int     `json:"ord"`
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
}

func privateCells(legs []Leg) []privateCell {
	out := []privateCell{}
	for _, l := range legs {
		for _, c := range l.Checkpoints {
			if c.Precision == precisionFuzzed {
				out = append(out, privateCell{Ord: c.Ord, Name: c.Name, Lat: c.Lat, Lng: c.Lng})
			}
		}
	}
	return out
}

// backTo returns a same-site Referer path, else the fallback.
func backTo(r *http.Request, fallback string) string {
	ref, err := url.Parse(r.Referer())
	if err != nil || ref.Path == "" || (ref.Host != "" && ref.Host != r.Host) {
		return fallback
	}
	return path.Clean(ref.Path)
}
