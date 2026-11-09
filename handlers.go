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
		"card": func(p Post, authed bool) postCard {
			// CanUnfuzz is a yes or no from the store; the kept point itself never
			// reaches a template.
			return postCard{Post: p, Authed: authed, CanUnfuzz: authed && p.PinFuzzed && a.store.CanUnfuzz(p.ID)}
		},
		"row": func(c Checkpoint, legOrd int, driver, chesterOn bool, nextID int64, showTZ bool) stopRow {
			return stopRow{Checkpoint: c, LegOrd: legOrd, Driver: driver, ChesterOn: chesterOn, Next: c.ID == nextID, ShowTZ: showTZ}
		},
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
	mux.Handle("POST /driver/telemetry", a.requireAuth(a.telemetry))
	mux.Handle("GET /driver/eta.json", a.requireAuth(a.etaJSON)) // every ETA shown, scored at arrival
	// The simulated drive: one server-held position per signed-in user (sim.go).
	mux.Handle("POST /driver/sim/start", a.requireAuth(a.simStart))
	mux.Handle("POST /driver/sim/stop", a.requireAuth(a.simStop))
	mux.Handle("GET /driver/sim.json", a.requireAuth(a.simJSON))
	mux.Handle("POST /driver/freelance/mode", a.requireAuth(a.freeMode))
	mux.Handle("POST /driver/freelance/dest", a.requireAuth(a.freeDest))
	mux.Handle("POST /driver/freelance/picks", a.requireAuth(a.freePicks))
	mux.Handle("GET /driver/roadtracks/search", a.requireAuth(a.roadtracksSearch))
	mux.Handle("GET /driver/roadtracks/album/{id}", a.requireAuth(a.roadtracksAlbum))
	mux.Handle("POST /driver/roadtracks/state", a.requireAuth(a.roadtracksState))
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
	mux.Handle("POST /driver/post/{id}/fuzz", a.requireRide(a.postFuzz))
	mux.Handle("POST /driver/post/{id}/unfuzz", a.requireRide(a.postUnfuzz))
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
	mux.Handle("POST /driver/routing", a.requireRide(a.routingAdd))
	// The checklist carriage, its signed-in census, and the check-off.
	mux.Handle("POST /driver/checklist", a.requireAuth(a.checklistPush))
	mux.Handle("GET /driver/checklist.json", a.requireRide(a.checklistCensus))
	mux.Handle("POST /driver/checklist/{kind}/{id}", a.requireRide(a.checklistMark))
	// Chester, the en-route helper, and its log. Signed in only, every one:
	// a question spends money and the log is the trip-keeper's own.
	mux.Handle("GET /chester", a.requireAuth(a.chesterPage))
	mux.Handle("POST /chester/ask", a.requireRide(a.chesterAsk))
	mux.Handle("GET /driver/chester/memory", a.requireAuth(a.chesterMemoryPage))
	mux.Handle("POST /driver/chester/memory/clear", a.requireAuth(a.chesterMemoryClear))
	mux.Handle("GET /driver/chester/log", a.requireAuth(a.chesterLog))
	mux.Handle("GET /driver/chester/log.json", a.requireAuth(a.chesterLogJSON))
	mux.Handle("POST /driver/chester/log/{id}", a.requireAuth(a.chesterVerdict))

	return logRequests(frameGuard(a.canonicalHost(a.visibility(a.shell(mux)))))
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
		http.Redirect(w, r, "/driver/login?next="+url.QueryEscape(dropEmbed(r.URL.RequestURI())), http.StatusFound)
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
				http.Redirect(w, r, "/driver/login?next="+url.QueryEscape(dropEmbed(r.URL.RequestURI())), http.StatusFound)
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
	// The page inside the player shell's frame: no card, no audio element.
	data["Embed"] = embedded(r)
	data["IsLogin"] = name == "login.html"
	// A simulated drive says so on every page, from the server, at render.
	data["SimAllowed"] = authed && a.simAllowed
	data["SimBanner"] = ""
	if authed {
		data["SimBanner"] = a.simBanner(r)
	}
	data["SimOn"] = data["SimBanner"] != ""
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
	a.chrome(r, name, data, authed, data["Driver"] == true)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if s, ok := data["Status"].(int); ok {
		w.WriteHeader(s) // after the headers above, which a WriteHeader would freeze out
	}
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
			if d.Total == 1 { // Kris ruled 2026-11-08: "Day 1 of 1" says nothing
				d.Badge = "Today"
			}
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
		if driveUnfinished(legs) { // a date alone never says the car got there
			d.Badge = "Still driving"
		}
		return d
	}
	d.Badge = "Day off"
	return d
}

// driveUnfinished says the drive began and its last stop is still open.
func driveUnfinished(legs []Leg) bool {
	var last *Checkpoint
	started := false
	for i := range legs {
		for j := range legs[i].Checkpoints {
			last = &legs[i].Checkpoints[j]
			started = started || last.ArrivedAt != "" || last.SkippedAt != ""
		}
	}
	return started && last.ArrivedAt == "" && last.SkippedAt == ""
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
	if di.Badge == "Arrived" || di.Badge == "Still driving" || di.Badge == "Day off" {
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
	// Hours are the whole-hour points ON the planned line and HoursLine the
	// stretch they measure, for the muted stick beside the route. Both are
	// published-line points; see hoursbar.go.
	Hours     []hourPoint `json:"hours,omitempty"`
	HoursLine [][]float64 `json:"hours_line,omitempty"`
}

type cpJS struct {
	ID         int64   `json:"id"`
	Ord        int     `json:"ord"`
	Mark       string  `json:"mark"`
	Name       string  `json:"name"`
	Label      string  `json:"label"`
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	TZ         string  `json:"tz"`
	Precision  string  `json:"precision"`
	LegSlug    string  `json:"leg_slug"`
	Planned    string  `json:"planned"` // local text
	Arrived    string  `json:"arrived"` // local text
	IsStart    bool    `json:"is_start,omitempty"`
	Skipped    bool    `json:"skipped,omitempty"`
	SkipReason string  `json:"skip_reason,omitempty"`
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
				Planned: planned, Arrived: localFmt(c.ArrivedAt, c.TZ, fmtLocal),
				IsStart: c.IsStart, Skipped: c.SkippedAt != "", SkipReason: c.SkipReason})
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
	bars := a.hoursBars(rv.Ride.Slug, rv.Legs, allIndexes(len(rv.Legs)), len(rv.Legs) > 1)
	data := map[string]any{
		"Title": "", "Ride": rv.Ride, "Legs": rv.Legs, "Posts": posts,
		"Day": di, "PageLeg": pageLeg, "Multi": len(rv.Legs) > 1,
		"NextID": nextOpenID(rv.Legs), "HoursBars": bars, "DriveStarted": driveStarted(rv.Legs),
		"Map": withHours(buildMap(rv.Legs, posts, legTracks(rv.Legs, posts), plannedStarts(rv.Legs, posts)), bars)}
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
	bars := a.hoursBars(rv.Ride.Slug, rv.Legs, []int{legIndex(rv.Legs, leg)}, false)
	a.render(w, r, "leg.html", map[string]any{
		"Title": leg.Name, "Ride": rv.Ride, "Leg": leg, "Prev": prev, "Next": next,
		"Posts": posts, "Day": di, "Total": len(rv.Legs), "Multi": len(rv.Legs) > 1,
		"NextID": nextOpenID(rv.Legs), "HoursBars": bars,
		// The tracks are computed over the whole ride so this leg's own track
		// still begins where the day before it ended.
		"Map":     withHours(buildMap([]Leg{*leg}, posts, legTracks(rv.Legs, posts), plannedStarts(rv.Legs, posts)), bars),
		"PageLeg": leg.Slug})
}

// stopRow is what the stop_row template is called with: the checkpoint plus
// what the row needs from the page. Next is true for the one stop that is a
// destination; only it offers navigation, and never the start.
type stopRow struct {
	Checkpoint
	LegOrd    int
	Driver    bool
	ChesterOn bool
	Next      bool
	ShowTZ    bool
}

// postCard is what the post_card template is called with: the post, plus
// whether the viewer is signed in. Post is embedded so every existing field
// reference in the template (.Kind, .Photo, …) is unchanged. The template's
// pin line — raw coordinates and the fuzz decision — is behind .Authed, and
// an anonymous render never contains it.
type postCard struct {
	Post
	Authed bool
	// CanUnfuzz: the post is fuzzed and a kept exact point exists (never the
	// point). CanFuzz: it has a published pin that is not fuzzed.
	CanUnfuzz bool
}

func (c postCard) CanFuzz() bool {
	return c.Authed && !c.HideLocation && !c.PinFuzzed && c.Lat != nil && c.PinSource != pinNone
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
	// A fuzzed post shows no exact point anywhere, the photo's own GPS included.
	if c.Photo != nil && c.Photo.Lat != nil && c.Photo.Lng != nil && !c.PinFuzzed {
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
	line, lead := "pin: ", "published "
	if hasFlag(c.Flag, flagOldPhoto) {
		// An old photo with no GPS of its own: the phone is not where it was
		// taken, so say how old it was and where its pin came from.
		if age := oldPhotoAge(c.Post); age != "" {
			line += "taken " + age + " ago, "
		}
		switch {
		case hasFlag(c.Flag, flagTrailPin):
			pub, lead = "pinned where the car was then ("+pub+")", ""
		case c.Lat == nil || c.PinSource == pinNone:
			pub, lead = "left off the map", ""
		}
	}
	if len(raw) > 0 {
		line += strings.Join(raw, ", ") + " → "
	}
	return line + lead + pub
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
				"is_terminal": c.IsTerminal, "mark": c.Mark, "is_start": c.IsStart}
			if c.SkippedAt != "" {
				p["skipped_at"] = c.SkippedAt
				p["skip_reason"] = c.SkipReason
			}
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
	okLogin := a.auth.Check(username, r.PostFormValue("password"))
	a.melt.Event("login", map[string]any{"user": username, "ok": okLogin})
	if !okLogin {
		w.WriteHeader(http.StatusUnauthorized)
		a.render(w, r, "login.html", map[string]any{"Title": "Sign in", "Error": "Incorrect username or password.",
			"Next": r.PostFormValue("next")})
		return
	}
	a.auth.Issue(w, a.secure, username)
	next := dropEmbed(r.PostFormValue("next"))
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
	a.renderPostForm(w, r, http.StatusOK, r.URL.Query().Get("error"))
}

// milestoneChoice is the one radio the post form can offer: Start the drive.
// Lat and Lng are the PUBLISHED position, the cell for a private place, which
// is all the page ever gets.
type milestoneChoice struct {
	ID       int64
	Name     string
	Label    string
	Lat, Lng float64
}

// checkinRadiusKm is how near a position must be to a stop to vouch for it.
const checkinRadiusKm = 10

// The words a post's Flag carries.
const (
	flagOutOfStep     = "out_of_step"
	flagTimeFromPhone = "time_from_phone"
)

// maxRoadKmh is the fastest a car is believed to have gone between two
// positions (250 km/h, about 155 mph). It is deliberately far above any road
// limit the trip meets: it exists to turn away a position no car could have
// reached (a photo's GPS from another city, a stale fix), never to police
// speed, and a false "out of step" on the road is worse than a missed one.
const maxRoadKmh = 250.0

// minGapHours is the shortest gap between two positions that is judged as
// itself: two photos a few seconds apart are judged as two minutes apart, so
// a fix that moved a little between them cannot read as impossible speed.
const minGapHours = 2.0 / 60

// futureSlack is how far ahead of the phone's clock a photo's own time may be
// before it is not believed. A camera a little fast is not a lie.
const futureSlack = 15 * time.Minute

// missedRadiusKm is how near the way the car came a skipped stop must lie to
// count as missed rather than rerouted around.
const missedRadiusKm = 15

// tripStart is the checkpoint the car leaves from: the first of the first leg.
// It is never a check-in; the drive is started there.
// driveStarted is true once the trip's start carries its departure stamp.
func driveStarted(legs []Leg) bool {
	st := tripStart(legs)
	return st != nil && st.ArrivedAt != ""
}

func tripStart(legs []Leg) *Checkpoint {
	if len(legs) == 0 {
		return nil
	}
	return legOrigin(legs, 0)
}

// openStops are the stops after the start that have neither an arrival nor a
// skip, in route order. The first is the next stop.
func openStops(legs []Leg) []Checkpoint {
	start := tripStart(legs)
	var out []Checkpoint
	for _, c := range rideCheckpoints(legs) {
		if (start != nil && c.ID == start.ID) || c.ArrivedAt != "" || c.SkippedAt != "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// nextOpenID is the id of the next stop: the first checkpoint in trip order
// that is not the start and is neither arrived at nor skipped. 0 when none is
// left. It is the one stop a page offers navigation to.
func nextOpenID(legs []Leg) int64 {
	if o := openStops(legs); len(o) > 0 {
		return o[0].ID
	}
	return 0
}

// vouch is the position a request proves, and who vouched for it.
type vouch struct {
	src      string // "sim", "photo" or "phone"
	lat, lng float64
	accM     float64 // the phone's own reported radius in metres; 0 when it gave none
}

// vouchFor resolves the position a post is made at: the simulated car first
// (only where SIM_ALLOWED, so a real photo never vouches on a KATE), then the
// photo's own GPS, then the phone's location the form attached. nil when there
// is none.
func (a *App) vouchFor(r *http.Request, exLat, exLng *float64) *vouch {
	if la, ln := a.simFix(r); la != nil {
		return &vouch{src: "sim", lat: *la, lng: *ln}
	}
	if exLat != nil && exLng != nil {
		return &vouch{src: "photo", lat: *exLat, lng: *exLng}
	}
	if la, ln := formCoord(r, "lat", 90), formCoord(r, "lng", 180); la != nil && ln != nil {
		v := &vouch{src: "phone", lat: *la, lng: *ln}
		if acc, err := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue("accuracy")), 64); err == nil && acc > 0 && acc < 1e6 {
			v.accM = acc
		}
		return v
	}
	return nil
}

// pointSegKm is the distance from a point to the segment a-b, on an
// equirectangular projection around the point: good to a fraction of a percent
// at the tens of kilometres it is used for.
func pointSegKm(pLat, pLng, aLat, aLng, bLat, bLng float64) float64 {
	const kmPerDeg = 111.195
	k := math.Cos(pLat * math.Pi / 180)
	ax, ay := (aLng-pLng)*k*kmPerDeg, (aLat-pLat)*kmPerDeg
	bx, by := (bLng-pLng)*k*kmPerDeg, (bLat-pLat)*kmPerDeg
	dx, dy := bx-ax, by-ay
	t := 0.0
	if d2 := dx*dx + dy*dy; d2 > 0 {
		t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/d2))
	}
	return math.Hypot(ax+t*dx, ay+t*dy)
}

// plannedStretch is the stretch of the planned route between two resolved
// points: the polyline the car was to follow, or, with no line to measure
// along, the straight chord between them.
type plannedStretch struct {
	pts [][2]float64
}

// nearKm is the least distance from a point to the stretch.
func (w plannedStretch) nearKm(lat, lng float64) float64 {
	best := math.Inf(1)
	for i := 1; i < len(w.pts); i++ {
		a, b := w.pts[i-1], w.pts[i]
		if d := pointSegKm(lat, lng, a[0], a[1], b[0], b[1]); d < best {
			best = d
		}
	}
	return best
}

// maxLineOffMi is how far a resolved point may sit from the planned line and
// still be measured along it; farther, the line says nothing of this stretch.
const maxLineOffMi = 25.0

// plannedWay is the planned route between the last resolved point and the new
// check-in: both are projected onto the line of the legs they lie on, and the
// polyline between the two projections is the stretch. It falls back to the
// chord when there is no line or either point lies far from it. A coastline
// like the 401 bows well away from the chord, which is why a chord is not the
// route.
func plannedWay(legs []Leg, from, to *Checkpoint) plannedStretch {
	chord := plannedStretch{pts: [][2]float64{{from.Lat, from.Lng}, {to.Lat, to.Lng}}}
	lo, hi := legIndexOf(legs, from.LegID), legIndexOf(legs, to.LegID)
	if lo < 0 || hi < 0 {
		return chord
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	var line [][2]float64
	for i := lo; i <= hi; i++ {
		line = append(line, linePoints(legs[i].Line)...)
	}
	if len(line) < 2 {
		return chord
	}
	a, offA := projectOnLine(line, from.Lat, from.Lng)
	b, offB := projectOnLine(line, to.Lat, to.Lng)
	if offA > maxLineOffMi || offB > maxLineOffMi {
		return chord
	}
	if a > b {
		a, b = b, a
	}
	stretch := [][2]float64{simWalkPathPoint(line, a)}
	cum := 0.0
	for i := 1; i < len(line); i++ {
		cum += simHaversine(line[i-1][0], line[i-1][1], line[i][0], line[i][1])
		if cum > a && cum < b {
			stretch = append(stretch, line[i])
		}
	}
	return plannedStretch{pts: append(stretch, simWalkPathPoint(line, b))}
}

func simWalkPathPoint(pts [][2]float64, d float64) [2]float64 {
	p, _ := simWalkPath(pts, d)
	return p
}

func legIndexOf(legs []Leg, id int64) int {
	for i := range legs {
		if legs[i].ID == id {
			return i
		}
	}
	return -1
}

// arrivalAt is when an arrival is stamped, by what vouched for it. Only a
// photo-vouched arrival (the photo's own GPS said the car was there) takes the
// photo's own time, read as wall-clock time in the stop's zone: loc is the zone
// the form read the photo's clock in. A phone vouches at the moment it speaks
// and the simulated car arrives now, so for them the photo's clock, which may
// be hours old, never dates the arrival. A photo time later than now (beyond
// futureSlack) is not believed: now is used and fromPhone says so.
func arrivalAt(photo *Photo, loc *time.Location, stopTZ, vouchedBy string) (at time.Time, fromPhone bool) {
	now := time.Now()
	if vouchedBy == "photo" && photo != nil && photo.TakenAt != "" {
		if t, ok := parseUTC(photo.TakenAt); ok {
			lt := t.In(loc)
			at = time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour(), lt.Minute(), lt.Second(), 0, loadLoc(stopTZ))
			if at.After(now.Add(futureSlack)) {
				return now, true
			}
			return at, false
		}
	}
	return now, false
}

// outOfStep says whether a car at (fromLat, fromLng) at fromAt could not have
// been at v by at. The reach is the road limit over the gap (never less than
// minGapHours) plus slack for what the positions do not know: the check-in
// radius (a stop is only known to within it, and a fuzzed one to its grid) and
// the fix's own accuracy. km is the distance between the two positions.
func outOfStep(fromLat, fromLng float64, fromAt, at time.Time, v *vouch) (out bool, km, gapMin float64) {
	km = haversineKm(fromLat, fromLng, v.lat, v.lng)
	gapH := math.Max(at.Sub(fromAt).Hours(), minGapHours)
	reach := maxRoadKmh*gapH + checkinRadiusKm + v.accM/1000
	return km > reach, km, at.Sub(fromAt).Minutes()
}

// checkInFromPhoto is what posting a photo does after the drive has started:
// nothing is chosen, the position decides. The nearest open stop within
// checkinRadiusKm is arrived at the photo's time and every open stop before it
// is skipped at the same instant: "missed" when it lies near the line from the
// last resolved point to here, "rerouted" when it does not. Returns the notice
// for the form, "" when there was nothing to say. The bus gets stops (ordinals)
// and sources, never a coordinate or a name.
func (a *App) checkInFromPhoto(r *http.Request, legs []Leg, post *Post, photo *Photo, loc *time.Location) (string, error) {
	open := openStops(legs)
	if len(open) == 0 {
		return "", nil
	}
	var exLat, exLng *float64
	if photo != nil {
		exLat, exLng = photo.Lat, photo.Lng
	}
	v := a.vouchFor(r, exLat, exLng)
	if v == nil {
		return "", nil
	}
	best, bestKm := -1, 0.0
	for i, c := range open {
		if km := haversineKm(v.lat, v.lng, c.Lat, c.Lng); km <= checkinRadiusKm && (best < 0 || km < bestKm) {
			best, bestKm = i, km
		}
	}
	if best < 0 {
		return fmt.Sprintf("Posted as a photo; next stop %s, %d km", open[0].Name,
			int(math.Round(haversineKm(v.lat, v.lng, open[0].Lat, open[0].Lng)))), nil
	}
	x := open[best]
	at, timeFromPhone := arrivalAt(photo, loc, x.TZ, v.src)
	// The last resolved point: the latest arrival, which is the start at worst.
	var last *Checkpoint
	all := rideCheckpoints(legs)
	for i := range all {
		if all[i].ArrivedAt != "" && (last == nil || all[i].ArrivedAt > last.ArrivedAt) {
			last = &all[i]
		}
	}
	// No arrival is earlier than the latest stamp on the ride, the start
	// included, whoever vouched, the simulated car too. A car can also only go
	// so far in the time; the simulated car is exempt from that alone, as it
	// exists only on a KATE and its posts are tests with library photos, not the
	// road. Whatever is decided here, the post is already saved and stays.
	var suffix string
	if timeFromPhone {
		suffix = "; the photo's time was ahead of the clock, so the phone's time was used"
		_ = a.store.AddPostFlag(post.ID, flagTimeFromPhone)
	}
	if last != nil {
		if lt, ok := parseUTC(last.ArrivedAt); ok {
			if at.Before(lt) {
				a.melt.Event("checkin_refused", map[string]any{"stop": x.Ord, "reason": "older_than_last_checkin", "vouched_by": v.src})
				return "Posted at its own time; it is older than the last check-in, so the car stays where it was" + suffix, nil
			}
			if out, km, gapMin := outOfStep(last.Lat, last.Lng, lt, at, v); out && v.src != "sim" {
				if err := a.store.AddPostFlag(post.ID, flagOutOfStep); err != nil {
					return "", err
				}
				a.melt.Event("checkin_refused", map[string]any{"stop": x.Ord, "reason": "out_of_step",
					"km": math.Round(km*10) / 10, "gap_min": math.Round(gapMin)})
				return fmt.Sprintf("Posted, and kept as out of step: %d km in %d min is further than a car goes, so nothing was checked in%s",
					int(math.Round(km)), int(math.Round(gapMin)), suffix), nil
			}
		}
	}
	way := plannedWay(legs, last, &x)
	var missed, rerouted []string
	for _, c := range open[:best] {
		reason := "rerouted"
		if last != nil && way.nearKm(c.Lat, c.Lng) <= missedRadiusKm {
			reason = "missed"
		}
		if err := a.store.SetSkipped(c.ID, at, reason); err != nil {
			return "", err
		}
		a.melt.Event("checkin_skipped", map[string]any{"stop": c.Ord, "reason": reason})
		if reason == "missed" {
			missed = append(missed, c.Name)
		} else {
			rerouted = append(rerouted, c.Name)
		}
	}
	if err := a.arriveCheckpoint(&x, at, nil); err != nil {
		return "", err
	}
	a.melt.Event("checkin_verified", map[string]any{"stop": x.Ord, "vouched_by": v.src, "km": math.Round(bestKm*10) / 10})
	log.Printf("checkin: stop %d (%s) reached by %s, %d skipped", x.Ord, x.Name, v.src, best)
	msg := "Reached " + x.Name + " at " + at.In(loadLoc(x.TZ)).Format("3:04 PM MST")
	if v.src == "sim" {
		msg += " by the simulated car"
	}
	if len(missed) > 0 {
		msg += "; missed " + strings.Join(missed, ", ")
	}
	if len(rerouted) > 0 {
		msg += "; rerouted around " + strings.Join(rerouted, ", ")
	}
	return msg + suffix, nil
}

// stepState is the stepping drive of the request's user once the drive has
// started: the one state a post moves. Only where SIM_ALLOWED.
func (a *App) stepState(r *http.Request, start *Checkpoint) (simState, bool) {
	if !a.simAllowed || a.sim == nil || start == nil || start.ArrivedAt == "" {
		return simState{}, false
	}
	st, ok := a.sim.State(a.userOf(r))
	return st, ok && st.Step
}

// simAtStopKm is how close the stepping car must be to a stop to count as
// standing at it.
const simAtStopKm = 0.5

// nextSimStep is where the stepping car goes on the next post: halfway to the
// next open stop along the planned line, and then to that stop, and so on. The
// next open stop is the first after the start that is neither arrived at nor
// skipped. A car standing at it needs no move; with no open stop left it stays
// put. Halfway and at-the-stop alternate by whether the car now stands at a
// stop (any stop, so a step past a skipped one starts again from halfway).
func nextSimStep(legs []Leg, cur LatLng) LatLng {
	open := openStops(legs)
	if len(open) == 0 {
		return cur
	}
	next := open[0]
	if haversineKm(cur.Lat, cur.Lng, next.Lat, next.Lng) <= simAtStopKm {
		return cur
	}
	atStop := false
	for _, c := range rideCheckpoints(legs) {
		if haversineKm(cur.Lat, cur.Lng, c.Lat, c.Lng) <= simAtStopKm {
			atStop = true
		}
	}
	// A hop of a kilometre or so has no halfway worth a post of its own.
	if !atStop || haversineKm(cur.Lat, cur.Lng, next.Lat, next.Lng) < 1 {
		return LatLng{next.Lat, next.Lng}
	}
	way := plannedWay(legs, &Checkpoint{Lat: cur.Lat, Lng: cur.Lng, LegID: next.LegID}, &next)
	mid, _ := simWalkPath(way.pts, simPathLength(way.pts)/2)
	return LatLng{mid[0], mid[1]}
}

// startChoice is the Start the drive choice while the drive has not started;
// nil once it has.
func startChoice(legs []Leg) *milestoneChoice {
	c := tripStart(legs)
	if c == nil || c.ArrivedAt != "" {
		return nil
	}
	return &milestoneChoice{ID: c.ID, Name: c.Name, Lat: c.Lat, Lng: c.Lng}
}

// nextStopName is the stop the next photo would check in at, "" before the
// drive starts or after the last stop.
func nextStopName(legs []Leg) string {
	if startChoice(legs) != nil {
		return ""
	}
	if o := openStops(legs); len(o) > 0 {
		return o[0].Name
	}
	return ""
}

func (a *App) renderPostForm(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
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
	// Only a Trip-mode driver starts a drive. In FreeLance Aimless or Destined
	// the form offers no start: starting would start the KATE's simulated car.
	start := startChoice(rv.Legs)
	if a.freeSvc().get(a.userOf(r)).Mode != modeTrip {
		start = nil
	}
	kind := "photo"
	if k := r.PostFormValue("kind"); validKind(k) {
		kind = k
	}
	data := map[string]any{
		"Title": "Post", "Ride": rv.Ride, "Legs": rv.Legs, "Kinds": postKinds,
		// This ride's addresses and no other's: an ord means nothing without its ride.
		"Private":      pd.forRide(rv.Ride.Slug, a.rideSlug).addressByOrd(),
		"DefaultLegID": dlID, "DefaultLeg": dl, "DefaultTZ": tz, "Zones": zones(rv.Legs, tz),
		"Day": di, "Error": errMsg, "Notice": r.URL.Query().Get("notice"),
		"NoticePost": a.noticePost(r, rv.Ride.Slug),
		"Start":      start, "NextStop": nextStopName(rv.Legs),
		"StartMode":  start != nil && r.URL.Query().Get("full") == "",
		"SimAllowed": a.simAllowed,
		"Kind":       kind,
		// What the form needs to say, before posting, what will be published:
		// the private cells the fuzz toggle pre-ticks itself for, and which way
		// PIN_PRECISION tips that default. These are the ALREADY-FUZZED
		// coordinates the public GeoJSON carries, not the real points.
		"PrivateCells": privateCells(rv.Legs), "PinPrecision": a.pinPrecision,
	}
	if dl != nil {
		data["PageLeg"] = dl.Slug
	}
	if status != http.StatusOK {
		data["Status"] = status
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
	pinSim        = "sim"
	pinBrowser    = "browser"
	pinCheckpoint = "checkpoint"
	pinNone       = "none"
)

// validPinSource guards the form's pin_source radio. "" on the wire means
// "nobody chose — run the ladder", which is also what a form served by a
// build from before the radio existed submits.
func validPinSource(v string) bool {
	switch v {
	case pinEXIF, pinSim, pinBrowser, pinCheckpoint, pinNone:
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
	// SimLat/Lng is the simulated car's position, set only where simulated
	// drives are allowed (a KATE) and one runs for the user. It beats every
	// other source but "No pin": a KATE must never hold a real position.
	SimLat, SimLng *float64
	CP             *Checkpoint
	// Old: the photo is an old one with no GPS of its own (old_photo.go), so
	// the phone is not where it was taken. The ladder then holds only the
	// trail, and TrailLat/Lng is the car's published position at the photo's
	// time, nil when the trail has none.
	Old                bool
	TrailLat, TrailLng *float64
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
	Trail        bool // the pin came from the car's trail (old_photo.go)
	// The point before it was fuzzed, kept privately so Unfuzz can restore it.
	// Set whenever Fuzzed is.
	ExactLat, ExactLng *float64
	ExactSource        string
	Note               string
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
		case pinSim:
			if in.SimLat != nil && in.SimLng != nil {
				return in.SimLat, in.SimLng, nil, true
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
		case pinTrail:
			if in.TrailLat != nil && in.TrailLng != nil {
				return in.TrailLat, in.TrailLng, nil, true
			}
		}
		return nil, nil, nil, false
	}

	chosen, fellBack := "", ""
	switch {
	case in.Want == pinNone:
		res.Note = "none (chosen)"
		return res
	case in.SimLat != nil && in.SimLng != nil:
		// The simulated car pins the post, whatever else was offered or chosen.
		chosen = pinSim
	case in.Want != "":
		if _, _, _, ok := have(in.Want); ok {
			chosen = in.Want
		} else {
			fellBack = in.Want
		}
	}
	if chosen == "" {
		ladder := []string{pinSim, pinEXIF, pinBrowser, pinCheckpoint}
		if in.Old {
			// The phone, the simulated car and the stop are not where an old
			// photo was taken: only the trail is.
			ladder = []string{pinTrail}
		}
		for _, src := range ladder {
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
	res.Trail = chosen == pinTrail
	if in.CP != nil {
		res.CheckpointID = in.CP.ID
	}
	switch chosen {
	case pinEXIF:
		res.Note = "exif"
	case pinSim:
		res.Note = "sim (the simulated car)"
	case pinBrowser:
		res.Note = "browser (no accuracy)"
		if acc != nil {
			res.Note = fmt.Sprintf("browser ±%.0f m", *acc)
		}
	case pinCheckpoint:
		res.Note = fmt.Sprintf("checkpoint %d (%s)", in.CP.Ord, in.CP.Name)
	case pinTrail:
		res.Note = "trail (the car's pin nearest the photo's time)"
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
	if res.Source != pinEXIF && res.Source != pinBrowser && res.Source != pinSim && res.Source != pinTrail {
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
	if res.Trail && pc != nil {
		// A point from the trail at a private place is never published as it
		// was, whatever the toggle or PIN_PRECISION says.
		fuzz = true
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
	exLat, exLng, exSrc := *res.Lat, *res.Lng, res.Source
	res.Fuzzed, res.AccuracyM = true, nil
	res.ExactLat, res.ExactLng, res.ExactSource = &exLat, &exLng, exSrc
	flat, flng, fsrc, fcp := fuzzedPoint(exLat, exLng, exSrc, pc)
	res.Lat, res.Lng, res.Source = &flat, &flng, fsrc
	if res.Trail {
		// A trail pin has no exact point of its own to keep for Unfuzz.
		res.ExactLat, res.ExactLng, res.ExactSource = nil, nil, ""
	}
	if pc != nil {
		// Name the place it belongs to, so the popup can say "near <name>" —
		// and move the post onto that checkpoint, as the pre-toggle build did.
		res.CheckpointID = fcp
		res.Note += fmt.Sprintf(" → fuzzed to checkpoint %d (%s)", pc.Ord, pc.Name)
		if precision == pinPrecisionExact {
			res.Note += " (fuzz on)"
		}
		return res
	}
	// Somewhere with no checkpoint of its own — a friend's driveway. Coarsen
	// the point to the grid and keep the source; there is no name to carry.
	res.Note += " → fuzzed to the 0.05° grid (no checkpoint)"
	return res
}

// fuzzedPoint is the one rule for "fuzz this point", shared by the form's
// toggle and the Fuzz This button: a point near a private stop goes to that
// stop's cell (its snapped point, and the post is filed under it); anywhere
// else it snaps to the 0.05 degree grid and keeps its source. pc is
// nearPrivate's answer for the point.
func fuzzedPoint(lat, lng float64, source string, pc *Checkpoint) (flat, flng float64, fsource string, checkpointID int64) {
	if pc != nil {
		return pc.Lat, pc.Lng, pinCheckpoint, pc.ID
	}
	return snap(lat), snap(lng), source, 0
}

// apply copies a resolved pin onto a post.
func (res pinResult) apply(p *Post) {
	p.Lat, p.Lng, p.PinSource, p.AccuracyM, p.PinFuzzed = res.Lat, res.Lng, res.Source, res.AccuracyM, res.Fuzzed
	p.exactLat, p.exactLng, p.exactSource = res.ExactLat, res.ExactLng, res.ExactSource
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
		// A form opened before the active trip changed carries a leg that is
		// not in this ride: the post lands where a new post lands by default
		// (the leg the form pre-selects), never a refusal the driver cannot act on.
		leg = defaultLeg(rv.Legs, dayOf(rv.Ride, rv.Legs, time.Now()))
	}
	if leg == nil {
		fail("Pick a leg.")
		return
	}

	// Starting the drive is the one thing the form can choose. After the drive
	// has started a post chooses nothing: its position decides (see
	// checkInFromPhoto), and an old form's milestone_id is ignored.
	start := tripStart(rv.Legs)
	starting := false
	if v := strings.TrimSpace(r.PostFormValue("milestone_id")); v != "" && start != nil && start.ArrivedAt == "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		starting = id == start.ID
	}
	// A start is vouched for BEFORE anything is saved, so a refusal leaves no
	// photo and no post behind. The EXIF is read from a second handle on the
	// upload; savePhoto reads the first.
	var startSrc string
	var startKm float64
	startSim := false
	if starting {
		var exLat, exLng *float64
		hasPhoto := false
		if f, _, err := r.FormFile("photo"); err == nil {
			hasPhoto = true
			if info := readEXIF(f); info.hasGPS {
				exLat, exLng = &info.lat, &info.lng
			}
			f.Close()
		}
		if !hasPhoto {
			a.renderPostForm(w, r, http.StatusBadRequest, "Starting the drive needs a photo")
			return
		}
		// A KATE driver may choose the phone or the photo's GPS over the
		// pretend car; a car already running would outrank both, so stop it.
		src := r.PostFormValue("start_source")
		if a.simAllowed && src == "gps" && a.sim != nil {
			a.sim.Stop(a.userOf(r))
		}
		switch v := a.vouchFor(r, exLat, exLng); {
		case a.simAllowed && src != "gps":
			// On a KATE starting the drive starts the pretend car from the
			// corridor's own origin: accepted, and the sim begins now.
			startSrc, startSim = "sim", true
		case v == nil:
			a.melt.Event("checkin_refused", map[string]any{"stop": start.Ord, "reason": "no_position"})
			a.renderPostForm(w, r, http.StatusBadRequest,
				"No position to start the drive at "+start.Name+": attach a photo with GPS or allow location")
			return
		default:
			startSrc, startKm = v.src, haversineKm(v.lat, v.lng, start.Lat, start.Lng)
			if startKm > checkinRadiusKm {
				a.melt.Event("checkin_refused", map[string]any{"stop": start.Ord, "reason": "too_far", "km": math.Round(startKm*10) / 10})
				a.renderPostForm(w, r, http.StatusBadRequest, fmt.Sprintf(
					"%s is %d km from where you are; start the drive when you are there", start.Name, int(math.Round(startKm))))
				return
			}
		}
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
	stepKept, timeFromPhone := false, false
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

	// On a KATE the stepping car moves one step per post, BEFORE the pin and the
	// vouch read it, so the post is pinned at, and checks in at, the new spot.
	// A note with no photo is not a check-in and does not move it.
	// A post that does not get saved must not have moved the car: the step is
	// put back unless CreatePost succeeds.
	if st, ok := a.stepState(r, start); ok && photo != nil {
		prev, user := st.StepPos, a.userOf(r)
		to := nextSimStep(rv.Legs, LatLng{prev[0], prev[1]})
		a.sim.StepTo(user, to.Lat, to.Lng)
		defer func() {
			if !stepKept {
				a.sim.StepTo(user, prev[0], prev[1])
			}
		}()
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
	pinFuzzed, pinFromTrail := false, false
	// An old photo with no GPS of its own (old_photo.go) was not taken where
	// the phone is: it is pinned from the car's trail or not at all, and it
	// never checks in. Start is a departure, not a check-in, and is unchanged.
	var oldTaken time.Time
	old := false
	if !starting {
		oldTaken, old = oldPhotoTime(photo, time.Now())
	}
	if !post.HideLocation {
		// The EXIF point is the one the SERVER read out of the uploaded file.
		// photo_lat/photo_lng came from the browser's own parse and are never
		// trusted here — they exist so the form could show the operator what
		// it found before posting.
		var exLat, exLng *float64
		if photo != nil {
			exLat, exLng = photo.Lat, photo.Lng
		}
		in := pinInputFrom(r, cp, exLat, exLng)
		// On a KATE the pretend car is where the phone is: it pins the post
		// ahead of the photo's own GPS, which is a real place.
		in.SimLat, in.SimLng = a.simFix(r)
		if starting && startSim {
			// The car only appears at the start once this post is saved, so
			// the start photo takes the start's own published point.
			lat, lng := start.Lat, start.Lng
			in.SimLat, in.SimLng = &lat, &lng
		}
		if old {
			in.Old = true
			// The page pre-selects a pin source by itself; only a radio the
			// driver touched (no pin_auto) is a choice, and that choice is
			// honoured for the pin alone.
			if r.PostFormValue("pin_auto") != "" {
				in.Want = ""
			}
			if in.Want != pinBrowser && in.Want != pinSim {
				in.SimLat, in.SimLng = nil, nil
			}
			if la, ln, ok := a.store.trailNear(rv.Ride.Slug, oldTaken); ok {
				in.TrailLat, in.TrailLng = &la, &ln
			}
		}
		res := resolvePin(in, rideCheckpoints(rv.Legs), a.pinPrecision)
		res.apply(post)
		pinNote = res.Note
		pinFuzzed, pinFromTrail = res.Fuzzed, res.Trail
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
		// A photo time later than the clock is not believed: the phone's is used.
		if t, ok := parseUTC(photo.TakenAt); ok && t.After(time.Now().Add(futureSlack)) {
			post.OccurredAt, timeFromPhone = utcString(time.Now()), true
		}
	default:
		post.OccurredAt = utcString(time.Now())
	}

	if err := a.store.CreatePost(post); err != nil {
		fail("Could not save: " + err.Error())
		return
	}
	stepKept = true
	if timeFromPhone {
		_ = a.store.AddPostFlag(post.ID, flagTimeFromPhone)
	}
	if old {
		_ = a.store.AddPostFlag(post.ID, flagOldPhoto)
		if pinFromTrail {
			_ = a.store.AddPostFlag(post.ID, flagTrailPin)
		}
	}
	log.Printf("post %d: pin %s", post.ID, pinNote)
	a.melt.Event("post_created", map[string]any{"pin_source": post.PinSource, "has_photo": photo != nil,
		"exif_gps": photo != nil && photo.Lat != nil, "fuzzed": pinFuzzed, "old_photo": old, "from_trail": pinFromTrail})
	if starting {
		// Start keeps the photo's clock: it is a departure, not a check-in.
		at, _ := arrivalAt(photo, loc, start.TZ, "photo")
		if err := a.arriveCheckpoint(start, at, nil); err != nil {
			fail("The post is saved, but the drive could not be started: " + err.Error())
			return
		}
		if startSim {
			a.startSteppingSim(a.userOf(r), start)
		}
		a.melt.Event("drive_started", map[string]any{"vouched_by": startSrc, "km": math.Round(startKm*10) / 10})
		log.Printf("post %d: drive started from %s by %s", post.ID, start.Name, startSrc)
		msg := "Drive started from " + start.Name + " at " + at.In(loadLoc(start.TZ)).Format("3:04 PM MST")
		if startSim {
			msg += " by the simulated car"
		}
		// A start is finished business: back to the driver screen, which says so.
		http.Redirect(w, r, "/driver?post="+strconv.FormatInt(post.ID, 10)+"&notice="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	if old {
		// Never a check-in, whatever the phone says: say what was done.
		msg := oldPhotoNotice(time.Since(oldTaken), post.Lat != nil, pinFromTrail)
		http.Redirect(w, r, "/driver/post?post="+strconv.FormatInt(post.ID, 10)+"&notice="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	if start != nil && start.ArrivedAt != "" && photo != nil {
		msg, err := a.checkInFromPhoto(r, rv.Legs, post, photo, loc)
		noticeURL := "/driver/post?post=" + strconv.FormatInt(post.ID, 10) + "&notice="
		if err != nil {
			fail("The post is saved, but the check-in failed: " + err.Error())
			return
		}
		if msg != "" {
			http.Redirect(w, r, noticeURL+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
	}
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

	var stampErr error
	if clearing {
		stampErr = a.store.SetArrived(id, at, loc)
	} else {
		stampErr = a.arriveCheckpoint(cp, at, loc)
	}
	if stampErr != nil {
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

// arriveCheckpoint is the one arrival path: it stamps the checkpoint, tells
// the bus how far off plan it was, and scores the ETAs the screen showed for
// the stop. The one-tap button, a back-dated repair and a photo's milestone
// option all come through here.
func (a *App) arriveCheckpoint(cp *Checkpoint, at time.Time, loc *LatLng) error {
	if err := a.store.SetArrived(cp.ID, at, loc); err != nil {
		return err
	}
	delta := 0
	if pa, err := time.Parse(time.RFC3339, cp.PlannedArrive); err == nil {
		delta = int(at.Sub(pa).Round(time.Minute) / time.Minute)
	}
	a.melt.Event("checkpoint_arrived", map[string]any{"name": cp.Name, "delta_min": delta})
	a.scoreETAs(cp, at)
	return nil
}

// scoreETAs scores the ETAs the screen showed for this stop and tells the bus
// how they did: names and minutes, never a position.
func (a *App) scoreETAs(cp *Checkpoint, at time.Time) {
	rows, err := a.store.ScoreETA(a.activeSlug(), cp.ID, at)
	if err != nil {
		log.Printf("eta ledger: scoring %s: %v", cp.Name, err)
		return
	}
	if len(rows) == 0 {
		return
	}
	latest, sum := rows[len(rows)-1], 0.0
	for _, r := range rows {
		sum += math.Abs(float64(*r.ErrorMin))
	}
	e := *latest.ErrorMin
	a.melt.Event("eta_hit", map[string]any{"checkpoint": cp.Name, "n": len(rows), "error_min": e,
		"mean_abs_error_min": math.Round(sum/float64(len(rows))*10) / 10, "within_10": e >= -10 && e <= 10})
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
	Flag         string `json:"flag,omitempty"`
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
			PinSource: p.PinSource, PinFuzzed: p.PinFuzzed, Flag: p.Flag, Lat: p.PinLat, Lng: p.PinLng}
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
	// While a simulated car runs (a KATE) the car is the only position: a
	// real point, a photo's own or a browser's, is never stored there, so a
	// re-pin may not choose one and the ladder may not fall to one.
	if a.simAllowed && a.sim != nil {
		if _, running := a.sim.State(a.userOf(r)); running {
			if in.Want == pinEXIF || in.Want == pinBrowser {
				http.Error(w, "on a KATE the simulated car is the only position", http.StatusBadRequest)
				return
			}
			in.ExifLat, in.ExifLng, in.BrowLat, in.BrowLng, in.Accuracy = nil, nil, nil, nil, nil
		}
	}
	post.HideLocation = in.Want == pinNone
	if post.HideLocation {
		post.Lat, post.Lng, post.PinSource, post.AccuracyM, post.PinFuzzed = nil, nil, pinNone, nil, false
		post.exactLat, post.exactLng, post.exactSource = nil, nil, ""
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

// noticePost is the post the notice on the form page is about, for its Fuzz
// This button: only a post of the active ride that has a pin and is not yet
// fuzzed, so the query cannot make the page offer a button for any other post.
func (a *App) noticePost(r *http.Request, rideSlug string) *Post {
	id, err := strconv.ParseInt(r.URL.Query().Get("post"), 10, 64)
	if err != nil {
		return nil
	}
	p, err := a.store.Post(id)
	if err != nil || p.LegSlug == "" || p.HideLocation || p.PinFuzzed || p.Lat == nil {
		return nil
	}
	if posts, err := a.store.Posts(rideSlug, p.LegID); err == nil {
		for _, q := range posts {
			if q.ID == p.ID {
				return p
			}
		}
	}
	return nil
}

// postFuzz is the one-tap Fuzz This on an existing post. It uses the same rule
// as the form's toggle (fuzzedPoint) and keeps the exact point privately for
// Unfuzz. A post with no pin, or one already fuzzed, is left as it is.
func (a *App) postFuzz(w http.ResponseWriter, r *http.Request) {
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
	if post.Lat != nil && post.Lng != nil && !post.HideLocation && !post.PinFuzzed {
		rv, err := a.loadRide()
		if err != nil {
			http.Error(w, "database error", 500)
			return
		}
		pc := nearPrivate(*post.Lat, *post.Lng, rideCheckpoints(rv.Legs))
		flat, flng, fsrc, fcp := fuzzedPoint(*post.Lat, *post.Lng, post.PinSource, pc)
		changed, err := a.store.FuzzPost(id, flat, flng, fsrc, fcp)
		if err != nil {
			http.Error(w, "could not fuzz", 500)
			return
		}
		if changed {
			log.Printf("post %d: fuzzed by Fuzz This", id)
			a.melt.Event("post_fuzzed", map[string]any{"post": id})
		}
	}
	http.Redirect(w, r, backTo(r, "/"), http.StatusSeeOther)
}

// postUnfuzz restores the exact point Fuzz This kept. With none kept it does
// nothing.
func (a *App) postUnfuzz(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := a.store.Post(id); err != nil {
		http.NotFound(w, r)
		return
	}
	changed, err := a.store.UnfuzzPost(id)
	if err != nil {
		http.Error(w, "could not unfuzz", 500)
		return
	}
	if changed {
		log.Printf("post %d: unfuzzed", id)
		a.melt.Event("post_unfuzzed", map[string]any{"post": id})
	}
	http.Redirect(w, r, backTo(r, "/"), http.StatusSeeOther)
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
