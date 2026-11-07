// roadtrip — a live ride surface: one trip, its legs and checkpoints from a
// seed file, and posts with photos from the phone as the miles go by.
//
// Single static binary + SQLite on a persistent volume, the EagleLakeRocks
// pattern. No framework, no runtime, nothing to patch from a car.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	// The image runs on scratch, which has no zoneinfo. Every time on this
	// site is rendered in a per-checkpoint IANA zone, so the zone database
	// must travel inside the binary.
	_ "time/tzdata"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

//go:embed seed/*.geojson
var seedFS embed.FS

// The ride's creature: Odo, a nine-banded armadillo (ruled by Kris
// 2026-09-01). static/img/creature.svg is still the placeholder until a
// SCOUT CODI mints the armadillo; that swap touches only the SVG.
const (
	CreatureName = "Odo"
	CreatureTag  = "Odo counts every mile."
	CreatureAlt  = "Odo, a nine-banded armadillo, counting the miles"
)

// LANCE sequences the legs — the spring-leader. His line on the leg pages.
const (
	GuideName = "Lance"
	GuideTag  = "Path. Order. Direction. Focus."
)

type App struct {
	// melt is the fleet observability sink; nil (off) when MELT_REDIS_ADDR is unset.
	melt     *meltSink
	tele     teleLimiter
	store    *Store
	auth     *Auth
	tmpl     *template.Template
	dataDir  string
	mediaDir string
	rideSlug string
	// assetV busts the static cache on deploy. Static assets are served with a
	// long max-age, so without a version in the URL a redeploy would leave
	// browsers running the previous build's JS and CSS against the new HTML.
	assetV string
	// secure marks cookies Secure; off only for plain-HTTP local runs.
	secure bool
	// siteURL is the public origin the pages name themselves by (canonical,
	// og:url) and the host every other host 301s to. It comes ONLY from
	// SITE_URL (fly.toml [env]); no hostname lives in the code. Empty — the
	// default, so `go run` and docker locally — means no redirect and no
	// canonical/og tags. /healthz is exempt from the redirect: Fly's check
	// hits the machine with an arbitrary Host and must keep getting 200.
	siteURL string
	// sitePrivate puts the WHOLE site behind the one admin password —
	// SITE_VISIBILITY=private, ruled by Kris 2026-09-02 as a temporary state
	// ("we will open it later"), so re-opening it is a config flip and a
	// redeploy. See handlers.go visibility for the exempt paths.
	sitePrivate bool
	// pinPrecision is PIN_PRECISION, and it decides exactly one thing: whether
	// the post form's "Fuzz location" toggle starts ticked at a PRIVATE place
	// ("snap", the default) or clear ("exact"). Everywhere else a point is
	// stored exactly in both modes, and the toggle overrides either way.
	pinPrecision string
	// chester is the en-route helper's client and settings, nil when it is
	// off: no ANTHROPIC_API_KEY, or a setting that does not parse. Every
	// Chester page asks this one field whether there is anything to offer.
	chester *Chester
	// chesterBusy is held for the length of one question. A second question
	// is refused while it is held (TryLock), never queued.
	chesterBusy sync.Mutex
	// hazards asks the weather and traffic sources for the driver screen. nil
	// (tests, mostly) behaves as a service with no TomTom key.
	hazards *hazardSvc
	// free is the FreeLance drive: per-user mode and destination, in memory,
	// and the TomTom search and routing it needs. nil is built on first use.
	free *freeSvc
	// tracks is the RoadTracks search; nil is the feature off.
	tracks *roadtracksSvc
}

// PIN_PRECISION values. See App.pinPrecision.
const (
	pinPrecisionSnap  = "snap"
	pinPrecisionExact = "exact"
)

// SITE_VISIBILITY values. See App.sitePrivate.
const (
	siteVisibilityPublic  = "public"
	siteVisibilityPrivate = "private"
)

func main() {
	hash := flag.String("hash", "", "print a bcrypt hash for the given password and exit")
	convert := flag.String("convert", "", "convert a planning-tool route document (schema roadtrip-route/0.1) at this path into a trip file and exit")
	cSlug := flag.String("slug", "", "with -convert: the ride slug (lowercase letters, digits, dashes)")
	cStart := flag.String("start", "", "with -convert: the day the ride starts, YYYY-MM-DD")
	cName := flag.String("name", "", "with -convert: the ride name; default is the route document's own")
	cZones := flag.String("zones", "", "with -convert: IANA zones for stops that carry no tz, as stop=Zone,stop=Zone")
	cOut := flag.String("out", "", "with -convert: where to write; default $DATA_DIR/trips/<slug>.geojson")
	setPw := flag.String("set-password", "", "set this user's password, read from stdin (one line), and exit")
	flag.Parse()
	if *setPw != "" {
		if err := setPasswordFile(env("DATA_DIR", "/data"), *setPw, os.Stdin); err != nil {
			log.Fatalf("set-password: %v", err)
		}
		fmt.Println("password set for", *setPw)
		return
	}
	if *hash != "" {
		h, err := HashPassword(*hash)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(h)
		return
	}
	if *convert != "" {
		out, err := runConvert(*convert, *cOut, env("DATA_DIR", "/data"),
			convertOptions{Slug: *cSlug, Start: *cStart, Name: *cName, Zones: *cZones})
		if err != nil {
			log.Fatalf("convert: %v", err)
		}
		fmt.Println(out)
		return
	}

	dataDir := env("DATA_DIR", "/data")
	addr := ":" + env("PORT", "8080")
	rideSlug := env("RIDE_SLUG", "atx-sjc-2026-09")

	mediaDir := filepath.Join(dataDir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	// scratch has no /tmp. Multipart uploads spill to disk past 1 MB
	// (handlers.go postCreate) and Go's temp files land in $TMPDIR, so point
	// it at the volume. Anything left behind by a request that died is swept
	// here on boot.
	tmpDir := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		log.Fatalf("tmp dir: %v", err)
	}
	os.Setenv("TMPDIR", tmpDir)
	sweepTemp(tmpDir,
		filepath.Join(mediaDir, ".upload-*"),
		filepath.Join(mediaDir, ".scrub-*"),
		filepath.Join(mediaDir, "*", "*", ".scrub-*"))
	setMemoryLimit()
	store, err := OpenStore(filepath.Join(dataDir, "roadtrip.sqlite3"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Once per volume: every original stored before the upload path started
	// stripping EXIF is rewritten in place without its location. Deliberately
	// not fatal — a boot must not be blocked by it — and the meta key is only
	// recorded on a clean pass, so a failure is retried on the next boot.
	if err := store.once("media_gps_stripped_v1", func() error {
		return sweepMediaGPS(store, mediaDir)
	}); err != nil {
		log.Printf("WARN media_gps_stripped_v1: %v", err)
	}

	// Seed and refresh from the embedded ride files on EVERY boot. A ride that
	// is not in the database is created; one that is has its planning data
	// refreshed — legs, checkpoints not yet arrived, candidate sights — while
	// arrived checkpoints, posts and photos are never touched
	// (spec-ride-rwgps-import-contract). All of this is deploy-coupled and
	// never time-coupled — see fly.toml for why that matters under suspend.
	seeds, _ := fs.Glob(seedFS, "seed/*.geojson")
	for _, name := range seeds {
		raw, err := seedFS.ReadFile(name)
		if err != nil {
			log.Fatalf("read seed %s: %v", name, err)
		}
		tmp := filepath.Join(dataDir, ".seed.geojson")
		if err := os.WriteFile(tmp, raw, 0o600); err != nil {
			log.Fatalf("stage seed: %v", err)
		}
		base := filepath.Base(name)
		base = base[:len(base)-len(filepath.Ext(base))]
		slug, created, err := store.Seed(tmp, base)
		if err != nil {
			log.Fatalf("seed %s: %v", name, err)
		}
		if created {
			log.Printf("seeded ride %s from %s", slug, name)
		} else if n, err := store.RefreshPlanning(tmp, base); err != nil {
			log.Fatalf("refresh planning %s: %v", name, err)
		} else {
			log.Printf("planning refreshed for ride %s from %s: %s", slug, name, n)
		}
		os.Remove(tmp)
	}

	// Then the trips on the volume, through the same create-or-refresh path.
	// These are the trips that are not in the image: the repository is public
	// and a trip's places and dates are not. A push applies a trip the moment
	// it lands (handlers: tripPush), so this boot pass is only what makes a
	// fresh machine or a restored volume come up whole — it is deploy-coupled
	// like everything above it, and nothing here depends on it re-running.
	if loaded, failed := loadVolumeTrips(store, dataDir); loaded+failed > 0 {
		log.Printf("volume trips: %d loaded, %d not loaded", loaded, failed)
	}
	// And each trip's checklist, after its trip: open steps take the file's
	// text, done ones are never written.
	if loaded, failed := loadVolumeChecklists(store, dataDir); loaded+failed > 0 {
		log.Printf("volume checklists: %d loaded, %d not loaded", loaded, failed)
	}

	app := &App{
		store:    store,
		auth:     NewAuth(),
		dataDir:  dataDir,
		mediaDir: mediaDir,
		rideSlug: rideSlug,
		assetV:   buildFingerprint(),
		secure:   env("COOKIE_SECURE", "1") == "1",
		siteURL:  strings.TrimRight(env("SITE_URL", ""), "/"),
	}
	visibility := strings.ToLower(env("SITE_VISIBILITY", siteVisibilityPublic))
	if visibility != siteVisibilityPublic && visibility != siteVisibilityPrivate {
		log.Fatalf("SITE_VISIBILITY %q: want %q or %q", visibility, siteVisibilityPublic, siteVisibilityPrivate)
	}
	app.sitePrivate = visibility == siteVisibilityPrivate
	app.pinPrecision = strings.ToLower(env("PIN_PRECISION", pinPrecisionSnap))
	if app.pinPrecision != pinPrecisionSnap && app.pinPrecision != pinPrecisionExact {
		log.Fatalf("PIN_PRECISION %q: want %q or %q", app.pinPrecision, pinPrecisionSnap, pinPrecisionExact)
	}
	app.tmpl = template.Must(template.New("").Funcs(app.funcs()).ParseFS(templateFS, "templates/*.html"))
	if app.siteURL != "" {
		if _, err := url.Parse(app.siteURL); err != nil {
			log.Fatalf("SITE_URL %q: %v", app.siteURL, err)
		}
		log.Printf("canonical host %s — other hosts 301 (except /healthz)", app.siteURL)
	}
	if app.sitePrivate {
		log.Printf("site visibility: private (all pages require sign-in except /healthz and the login)")
	} else {
		log.Printf("site visibility: public")
	}
	if app.pinPrecision == pinPrecisionExact {
		log.Printf("pin precision: exact (a pin at a private place publishes as taken unless Fuzz location is ticked)")
	} else {
		log.Printf("pin precision: snap (a pin at a private place is fuzzed unless Fuzz location is unticked)")
	}
	if err := app.auth.LoadUsers(dataDir); err != nil {
		log.Fatalf("users: %v", err)
	}
	if !app.auth.enabled() {
		log.Printf("WARN no users and ADMIN_PASSWORD_HASH unset — posting is disabled, site is read-only")
	}
	// Whether the header path exists at all, never the token itself — this log
	// ships to flyctl logs, and a credential that reaches a log is spent.
	if len(app.auth.pushToken) > 0 {
		log.Printf("push token: set (Authorization: Bearer … is accepted wherever the session is)")
	} else {
		log.Printf("push token: unset (the session cookie is the only way in)")
	}
	// Private overlay, if an operator has placed one on the volume. Absent is
	// normal and silent-by-design; a malformed file is worth a warning.
	if pd, err := loadPrivate(dataDir); err != nil {
		log.Printf("WARN private overlay: %v", err)
	} else if n := pd.entries(); n > 0 {
		log.Printf("private overlay: %d checkpoint entr(ies) across %d ride(s) (admin pages only)", n, len(pd.Rides)+1)
	}
	// Chester: on only with a key and settings that parse. The log line names
	// the models and the day's limit and never the key; a credential that
	// reaches a log is spent.
	// The driver screen's hazard sources. No TOMTOM_API_KEY is a supported
	// state: the traffic chip then says so instead of staying quiet.
	app.melt = newMelt(os.Getenv("MELT_REDIS_ADDR"), os.Getenv("MELT_VENUE"))
	app.hazards = newHazardSvc(os.Getenv("TOMTOM_API_KEY"))
	app.free = newFreeSvc(os.Getenv("TOMTOM_API_KEY"))
	if os.Getenv("TOMTOM_API_KEY") == "" {
		log.Print("traffic: TOMTOM_API_KEY unset, the driver screen will say traffic is unknown")
	} else {
		log.Print("traffic: TOMTOM_API_KEY set")
	}
	if app.tracks = newRoadtracks(os.Getenv("ROADTRACKS_TOKEN"), os.Getenv("ROADTRACKS_BASE")); app.tracks == nil {
		log.Print("roadtracks: ROADTRACKS_TOKEN unset, the RoadTracks card is off")
	} else {
		log.Print("roadtracks: enabled")
	}
	if cfg, why := chesterConfigFrom(os.Getenv); cfg == nil {
		log.Printf("chester: disabled (%s)", why)
	} else {
		app.chester = newChester(cfg)
		log.Printf("chester: enabled (quick %s at %s effort, plan %s at %s effort, limit $%.2f a day UTC)",
			cfg.Quick.Model, cfg.Quick.Effort, cfg.Plan.Model, cfg.Plan.Effort, cfg.DailyUSD)
		if cfg.baseURL != "" {
			log.Printf("WARN chester: ANTHROPIC_BASE_URL is set, so questions go to that address and not to the API")
		}
		for _, mode := range []string{chesterQuick, chesterPlan} {
			if worst := cfg.mode(mode).worstUSD(); chesterOverLimit(0, worst, cfg.DailyUSD) {
				log.Printf("WARN chester: a %s question can cost up to $%.2f, above the day's limit, so every %s question will be refused",
					mode, worst, mode)
			}
		}
	}
	if _, err := store.Ride(rideSlug); err != nil {
		log.Printf("WARN RIDE_SLUG=%s is not in the database — / will 404 until it is seeded", rideSlug)
	}
	if active := app.activeSlug(); active != rideSlug {
		log.Printf("active ride: %s (set by the trip-keeper; RIDE_SLUG=%s is the fallback)", active, rideSlug)
	}
	// Drivers written before ride assignment keep the ride the site shows now.
	if n, err := app.auth.users.migrateLegacyRides(app.activeSlug()); err != nil {
		log.Printf("WARN users: could not pin existing drivers to ride %s: %v", app.activeSlug(), err)
	} else if n > 0 {
		log.Printf("users: %d existing driver(s) assigned to the active ride %s", n, app.activeSlug())
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// Uploads from a hotspot can be slow; 20 MB at 1 Mbit/s is ~3 min.
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("roadtrip listening on %s (ride %s)", addr, app.activeSlug())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Print("shut down cleanly")
}

// buildFingerprint identifies this build. It uses the executable's mtime, which
// changes on every deploy and is stable across a suspend/resume cycle.
func buildFingerprint() string {
	exe, err := os.Executable()
	if err != nil {
		return "dev"
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return "dev"
	}
	return strconv.FormatInt(fi.ModTime().Unix(), 36)
}

// setMemoryLimit gives the Go GC a soft ceiling below the machine's RAM so it
// collects harder before the kernel OOM-kills the process — which is what
// happened to a phone photo post on the 256 MB VM on 2026-09-02. A GOMEMLIMIT
// in the environment wins (the runtime already honours it). Otherwise the
// ceiling is 75% of the cgroup limit when one is set (docker --memory), else
// 75% of MemTotal (a Fly VM has no cgroup limit; the VM is the limit).
func setMemoryLimit() {
	if os.Getenv("GOMEMLIMIT") != "" {
		log.Printf("memory limit: GOMEMLIMIT=%s", os.Getenv("GOMEMLIMIT"))
		return
	}
	var total int64
	src := "cgroup"
	if b, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		total, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	}
	if total <= 0 {
		src = "MemTotal"
		if b, err := os.ReadFile("/proc/meminfo"); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(line, "MemTotal:") {
					f := strings.Fields(line)
					if len(f) >= 2 {
						kb, _ := strconv.ParseInt(f[1], 10, 64)
						total = kb << 10
					}
				}
			}
		}
	}
	if total <= 0 {
		log.Printf("memory limit: none (no cgroup limit or MemTotal readable)")
		return
	}
	limit := total / 4 * 3
	debug.SetMemoryLimit(limit)
	log.Printf("memory limit: %d MiB (75%% of %d MiB %s)", limit>>20, total>>20, src)
}

// sweepTemp removes leftovers from work that died half-done: everything in
// the multipart spill dir, plus anything matching the given globs —
// half-written .upload-* files in media/, and .scrub-* files an interrupted
// EXIF rewrite left beside an original.
func sweepTemp(tmpDir string, globs ...string) {
	n := 0
	if ents, err := os.ReadDir(tmpDir); err == nil {
		for _, e := range ents {
			if os.RemoveAll(filepath.Join(tmpDir, e.Name())) == nil {
				n++
			}
		}
	}
	for _, g := range globs {
		m, err := filepath.Glob(g)
		if err != nil {
			continue
		}
		for _, f := range m {
			if os.Remove(f) == nil {
				n++
			}
		}
	}
	if n > 0 {
		log.Printf("swept %d temp file(s) left by an interrupted upload", n)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
