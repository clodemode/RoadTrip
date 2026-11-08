package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Ride is one trip: a slug, a name, a date span, and the two ends.
type Ride struct {
	Slug          string
	Name          string
	StartDate     string // YYYY-MM-DD, local to the origin
	EndDate       string // YYYY-MM-DD, local to the terminus
	Mode          string // CAR
	OriginLabel   string
	TerminusLabel string
}

// Leg is one day's drive. Line is the planned polyline as GeoJSON
// coordinates ([lng,lat] pairs) so the map can draw it without re-parsing.
type Leg struct {
	ID        int64           `json:"id"`
	RideSlug  string          `json:"-"`
	Ord       int             `json:"ord"`
	Slug      string          `json:"slug"`
	Name      string          `json:"name"`
	Date      string          `json:"date"`
	EstMiles  float64         `json:"est_miles"`
	EstHours  float64         `json:"est_hours"`
	RouteHint string          `json:"route_hint"`
	Line      json.RawMessage `json:"line"`
	// TZ is the zone the leg STARTS in — its first checkpoint's zone. Set at
	// load. It decides what "today" means for this leg's day-of-trip badge.
	TZ string `json:"tz"`
	// Filled by the handlers for page rendering.
	Checkpoints []Checkpoint `json:"-"`
	Sights      []Sight      `json:"-"`
}

// Checkpoint is a named point on a leg with its own time zone. The zone is
// per-checkpoint, not per-ride: this trip crosses CDT → MDT → MST (Arizona,
// no DST) → PDT, and a single ride-wide zone would misprint every time after
// the first border.
//
// Precision "fuzzed" means the point is private (the origin is a home). The
// store snaps such a point to the 0.05° grid on the way OUT, so nothing
// downstream — pages, GeoJSON, pins — ever sees the real coordinate.
type Checkpoint struct {
	ID            int64   `json:"id"`
	LegID         int64   `json:"leg_id"`
	Ord           int     `json:"ord"`
	Name          string  `json:"name"`
	Label         string  `json:"label"`
	Lat           float64 `json:"lat"`
	Lng           float64 `json:"lng"`
	TZ            string  `json:"tz"`
	Precision     string  `json:"precision"`
	IsTerminal    bool    `json:"is_terminal"`
	PlannedArrive string  `json:"planned_arrive,omitempty"` // RFC3339 UTC or ""
	PlannedDepart string  `json:"planned_depart,omitempty"`
	ArrivedAt     string  `json:"arrived_at,omitempty"`
	// SkippedAt is set when the driver passed this stop without checking in:
	// "missed" (it lay near the way the car came) or "rerouted" (it did not).
	// A skipped stop is passed, never arrived: nothing reads it as an arrival.
	SkippedAt  string `json:"skipped_at,omitempty"`
	SkipReason string `json:"skip_reason,omitempty"`
	// Mark is the label a map pin and a list row carry: a letter
	// (A, B, C…) for a landmark or milestone, or "d2" for the end of driving
	// day 2 on a ride of several days. Numbers are reserved for days and carry
	// their unit. Set by markCheckpoints, never stored.
	Mark string `json:"-"`
	// IsStart is true for the trip's first checkpoint, where the car leaves
	// from. Set by markCheckpoints, never stored: Ord is per leg, so it cannot
	// say which stop is the start.
	IsStart bool `json:"-"`
}

// Checkpoint precision. "fuzzed" is the privacy seam: the store snaps such a
// point on the way out and never hands out the real one. "exact" is what an
// arrival with a real location leaves behind at a public place — a motel pin
// is the motel, not the town centroid the plan guessed at.
const (
	precisionApprox = "approx"
	precisionFuzzed = "fuzzed"
	precisionExact  = "exact"
)

// LatLng is one published point: what an arrival adopts and what the store
// then writes onto the checkpoint. Published, not raw — see SetArrived.
type LatLng struct{ Lat, Lng float64 }

// Sight is a candidate stop. Informational only — the seed says so.
type Sight struct {
	ID    int64   `json:"id"`
	LegID int64   `json:"leg_id"`
	Name  string  `json:"name"`
	Why   string  `json:"why"`
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Kind  string  `json:"kind"`
}

// Place is a public town name along the route (a trip document's Point with
// kind=place). It is never drawn on the map and never offered as a stop: the
// hours bar uses it only to say where the car should be at a whole hour, and
// the page never carries its coordinate.
type Place struct {
	Name     string
	Lat, Lng float64
}

// Photo is an uploaded JPEG plus what its EXIF said.
type Photo struct {
	ID        int64
	Path      string // relative to the media dir, e.g. 2026/09/ab12….jpg
	ThumbPath string
	TakenAt   string // RFC3339 UTC or ""
	TZ        string
	Lat       *float64
	Lng       *float64
	Width     int
	Height    int
	SHA256    string
}

// Post is one timeline entry. OccurredAt is UTC; TZ is the IANA zone it is
// rendered in.
type Post struct {
	ID           int64
	LegID        int64
	CheckpointID int64 // 0 = none
	Kind         string
	OccurredAt   string // RFC3339 UTC
	TZ           string
	Title        string
	Body         string
	PhotoID      int64 // 0 = none
	HideLocation bool
	CreatedAt    string
	// The pin, resolved at post time (App.postCreate), re-chosen later
	// (App.postRepin) or set by the one-shot backfill (Store.backfillPins):
	// Lat/Lng nil = no pin. PinSource is one of exif | browser | checkpoint |
	// none. AccuracyM is the phone's reported radius, browser pins only. A
	// hidden post stores nothing here.
	//
	// PinFuzzed marks a post whose raw fix was deliberately coarsened by the
	// form's "Fuzz location" toggle: the point is on the 0.05° grid and the
	// accuracy is gone. It exists so a fuzzed — or, by its absence, an exact —
	// post is findable later; nothing renders it to the public.
	Lat       *float64
	Lng       *float64
	PinSource string
	AccuracyM *float64
	PinFuzzed bool
	// The exact point a fuzzed post had, kept so Unfuzz can restore it. Set
	// only by resolvePin (via pinResult.apply) for CreatePost and
	// UpdatePostPin to write. Unexported, so no JSON or template can carry it,
	// and never read back into a Post: the columns are in no post select.
	exactLat, exactLng *float64
	exactSource        string
	// Joined for rendering.
	LegSlug        string
	LegName        string
	LegOrd         int
	CheckpointName string
	Photo          *Photo
	// Pin position, if the post has one (see Store.pin).
	PinLat *float64
	PinLng *float64
}

const schema = `
CREATE TABLE IF NOT EXISTS rides (
  slug           TEXT PRIMARY KEY,
  name           TEXT NOT NULL,
  start_date     TEXT NOT NULL DEFAULT '',
  end_date       TEXT NOT NULL DEFAULT '',
  mode           TEXT NOT NULL DEFAULT 'CAR',
  origin_label   TEXT NOT NULL DEFAULT '',
  terminus_label TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS legs (
  id           INTEGER PRIMARY KEY,
  ride_slug    TEXT NOT NULL REFERENCES rides(slug) ON DELETE CASCADE,
  ord          INTEGER NOT NULL,
  slug         TEXT NOT NULL,
  name         TEXT NOT NULL,
  date         TEXT NOT NULL,
  est_miles    REAL NOT NULL DEFAULT 0,
  est_hours    REAL NOT NULL DEFAULT 0,
  route_hint   TEXT NOT NULL DEFAULT '',
  line_geojson TEXT NOT NULL DEFAULT '',
  UNIQUE (ride_slug, slug)
);
CREATE TABLE IF NOT EXISTS checkpoints (
  id             INTEGER PRIMARY KEY,
  leg_id         INTEGER NOT NULL REFERENCES legs(id) ON DELETE CASCADE,
  ord            INTEGER NOT NULL,
  name           TEXT NOT NULL,
  label          TEXT NOT NULL DEFAULT '',
  lat            REAL NOT NULL,
  lng            REAL NOT NULL,
  tz             TEXT NOT NULL,
  precision      TEXT NOT NULL DEFAULT 'approx',
  is_terminal    INTEGER NOT NULL DEFAULT 0,
  planned_arrive TEXT NOT NULL DEFAULT '',
  planned_depart TEXT NOT NULL DEFAULT '',
  arrived_at     TEXT NOT NULL DEFAULT '',
  skipped_at     TEXT NOT NULL DEFAULT '',
  skip_reason    TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS sights (
  id     INTEGER PRIMARY KEY,
  leg_id INTEGER NOT NULL REFERENCES legs(id) ON DELETE CASCADE,
  name   TEXT NOT NULL,
  why    TEXT NOT NULL DEFAULT '',
  lat    REAL NOT NULL,
  lng    REAL NOT NULL,
  kind   TEXT NOT NULL DEFAULT 'SIGHT'
);
CREATE TABLE IF NOT EXISTS places (
  id        INTEGER PRIMARY KEY,
  ride_slug TEXT NOT NULL REFERENCES rides(slug) ON DELETE CASCADE,
  name      TEXT NOT NULL,
  lat       REAL NOT NULL,
  lng       REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS places_ride ON places(ride_slug);
CREATE TABLE IF NOT EXISTS photos (
  id         INTEGER PRIMARY KEY,
  path       TEXT NOT NULL,
  thumb_path TEXT NOT NULL,
  taken_at   TEXT NOT NULL DEFAULT '',
  tz         TEXT NOT NULL DEFAULT '',
  lat        REAL,
  lng        REAL,
  width      INTEGER NOT NULL DEFAULT 0,
  height     INTEGER NOT NULL DEFAULT 0,
  sha256     TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS posts (
  id            INTEGER PRIMARY KEY,
  leg_id        INTEGER NOT NULL REFERENCES legs(id) ON DELETE CASCADE,
  checkpoint_id INTEGER REFERENCES checkpoints(id) ON DELETE SET NULL,
  kind          TEXT NOT NULL DEFAULT 'note',
  occurred_at   TEXT NOT NULL,
  tz            TEXT NOT NULL,
  title         TEXT NOT NULL DEFAULT '',
  body          TEXT NOT NULL DEFAULT '',
  photo_id      INTEGER REFERENCES photos(id) ON DELETE SET NULL,
  hide_location INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  lat           REAL,
  lng           REAL,
  pin_source    TEXT NOT NULL DEFAULT 'none',
  accuracy_m    REAL,
  pin_fuzzed    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS posts_occurred ON posts(occurred_at);
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS eta_predictions (
  id                INTEGER PRIMARY KEY,
  ride_slug         TEXT NOT NULL,
  checkpoint_id     INTEGER NOT NULL,
  user              TEXT NOT NULL DEFAULT '',
  predicted_at      TEXT NOT NULL,
  predicted_arrival TEXT NOT NULL,
  source            TEXT NOT NULL DEFAULT 'plan',
  actual_arrival    TEXT,
  error_min         INTEGER
);
CREATE INDEX IF NOT EXISTS eta_predictions_stop ON eta_predictions(ride_slug, checkpoint_id);
CREATE TABLE IF NOT EXISTS checklists (
  ride_slug  TEXT PRIMARY KEY REFERENCES rides(slug) ON DELETE CASCADE,
  title      TEXT NOT NULL DEFAULT '',
  intro      TEXT NOT NULL DEFAULT '',
  disclaimer TEXT NOT NULL DEFAULT '',
  sources    TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS checklist_stages (
  id        INTEGER PRIMARY KEY,
  ride_slug TEXT NOT NULL REFERENCES rides(slug) ON DELETE CASCADE,
  stage_id  TEXT NOT NULL,
  ord       INTEGER NOT NULL,
  title     TEXT NOT NULL DEFAULT '',
  tz        TEXT NOT NULL,
  when_text TEXT NOT NULL DEFAULT '',
  UNIQUE (ride_slug, stage_id)
);
CREATE TABLE IF NOT EXISTS checklist_items (
  id           INTEGER PRIMARY KEY,
  ride_slug    TEXT NOT NULL REFERENCES rides(slug) ON DELETE CASCADE,
  kind         TEXT NOT NULL,
  item_id      TEXT NOT NULL,
  stage_id     TEXT NOT NULL DEFAULT '',
  grp          TEXT NOT NULL DEFAULT '',
  ord          INTEGER NOT NULL,
  title        TEXT NOT NULL DEFAULT '',
  detail       TEXT NOT NULL DEFAULT '',
  private      TEXT NOT NULL DEFAULT '',
  done_at      TEXT NOT NULL DEFAULT '',
  done_tz      TEXT NOT NULL DEFAULT '',
  outcome      TEXT NOT NULL DEFAULT '',
  private_note TEXT NOT NULL DEFAULT '',
  UNIQUE (ride_slug, kind, item_id)
);
CREATE TABLE IF NOT EXISTS chester_queries (
  id              INTEGER PRIMARY KEY,
  ride_slug       TEXT NOT NULL DEFAULT '',
  asked_at        TEXT NOT NULL,
  tz              TEXT NOT NULL DEFAULT 'UTC',
  mode            TEXT NOT NULL,
  preset          TEXT NOT NULL DEFAULT '',
  question        TEXT NOT NULL,
  prompt_version  TEXT NOT NULL DEFAULT '',
  model           TEXT NOT NULL,
  served_model    TEXT NOT NULL DEFAULT '',
  effort          TEXT NOT NULL DEFAULT '',
  first_ms        INTEGER,
  total_ms        INTEGER NOT NULL DEFAULT 0,
  tokens_in       INTEGER NOT NULL DEFAULT 0,
  tokens_out      INTEGER NOT NULL DEFAULT 0,
  cache_read      INTEGER NOT NULL DEFAULT 0,
  cache_write     INTEGER NOT NULL DEFAULT 0,
  searches        INTEGER NOT NULL DEFAULT 0,
  usd             REAL NOT NULL DEFAULT 0,
  answer          TEXT NOT NULL DEFAULT '',
  places          TEXT NOT NULL DEFAULT '[]',
  stop_reason     TEXT NOT NULL DEFAULT '',
  error           TEXT NOT NULL DEFAULT '',
  note            TEXT NOT NULL DEFAULT '',
  pos_lat         REAL,
  pos_lng         REAL,
  pos_source      TEXT NOT NULL DEFAULT 'none',
  pos_label       TEXT NOT NULL DEFAULT '',
  verdict         TEXT NOT NULL DEFAULT '',
  checked_against TEXT NOT NULL DEFAULT '',
  verdict_note    TEXT NOT NULL DEFAULT '',
  chester_value   REAL,
  reference_value REAL,
  value_unit      TEXT NOT NULL DEFAULT '',
  verdict_at      TEXT NOT NULL DEFAULT '',
  partial         TEXT NOT NULL DEFAULT '',
  usd_estimated   INTEGER NOT NULL DEFAULT 0,
  answer_synth    INTEGER NOT NULL DEFAULT 0,
  usage_json      TEXT NOT NULL DEFAULT '[]',
  user            TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS chester_asked ON chester_queries(asked_at);
CREATE TABLE IF NOT EXISTS chester_turns (
  id    INTEGER PRIMARY KEY,
  user  TEXT NOT NULL,
  at    TEXT NOT NULL,
  q     TEXT NOT NULL,
  a     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS chester_turns_user ON chester_turns(user, id);
CREATE TABLE IF NOT EXISTS chester_leanings (
  id    INTEGER PRIMARY KEY,
  user  TEXT NOT NULL,
  at    TEXT NOT NULL,
  note  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS chester_leanings_user ON chester_leanings(user, id);
`

type Store struct{ db *sql.DB }

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// SQLite tolerates exactly one writer; serialising here avoids SQLITE_BUSY
	// entirely on a single-machine deployment.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrate is where a later build adds columns. CREATE TABLE IF NOT EXISTS is
// a no-op on an existing table, so every new column needs an explicit ALTER
// here, guarded by PRAGMA table_info so a re-run is a no-op. One-shot data
// fixes are keyed in the meta table and run exactly once per volume. All of
// this is deploy-coupled boot work (see fly.toml on suspend).
func (s *Store) migrate() error {
	if err := s.addColumns("posts", []string{
		"lat REAL", "lng REAL", "pin_source TEXT NOT NULL DEFAULT 'none'", "accuracy_m REAL",
		"pin_fuzzed INTEGER NOT NULL DEFAULT 0",
		// The exact point a post had before Fuzz This. Private: no post read
		// selects these; see FuzzPost and UnfuzzPost.
		"exact_lat REAL", "exact_lng REAL", "exact_source TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return err
	}
	if err := s.addColumns("checkpoints", []string{
		"skipped_at TEXT NOT NULL DEFAULT ''", "skip_reason TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return err
	}
	if err := s.addColumns("chester_queries", []string{
		"partial TEXT NOT NULL DEFAULT ''", "usd_estimated INTEGER NOT NULL DEFAULT 0",
		"answer_synth INTEGER NOT NULL DEFAULT 0", "usage_json TEXT NOT NULL DEFAULT '[]'",
		"user TEXT NOT NULL DEFAULT ''", "proposal TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return err
	}
	if err := s.chesterFirstWordNullable(); err != nil {
		return err
	}
	if err := s.once("pins_backfilled_v1", s.backfillPins); err != nil {
		return err
	}
	return s.once("pins_fuzzed_near_private_v1", s.fuzzPinsNearPrivate)
}

// addColumns ALTERs in each "name TYPE ..." definition the table lacks.
func (s *Store) addColumns(table string, defs []string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, d := range defs {
		name := strings.Fields(d)[0]
		if have[name] {
			continue
		}
		if _, err := s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + d); err != nil {
			return fmt.Errorf("add column %s.%s: %w", table, name, err)
		}
		log.Printf("migrate: added column %s.%s", table, name)
	}
	return nil
}

// chesterFirstWordNullable lets chester_queries.first_ms be empty. The column
// was first made NOT NULL DEFAULT 0, which wrote a wait of zero milliseconds
// for an answer that had no words at all. A table of that shape is given a
// column that can be empty, and its zeros, which never meant a time, become
// empty. A table that already has the new shape is left alone.
func (s *Store) chesterFirstWordNullable() error {
	var notNull int
	err := s.db.QueryRow(`SELECT "notnull" FROM pragma_table_info('chester_queries') WHERE name='first_ms'`).Scan(&notNull)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && notNull == 0) {
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`ALTER TABLE chester_queries RENAME COLUMN first_ms TO first_ms_v1`,
		`ALTER TABLE chester_queries ADD COLUMN first_ms INTEGER`,
		`UPDATE chester_queries SET first_ms = NULLIF(first_ms_v1, 0)`,
		`ALTER TABLE chester_queries DROP COLUMN first_ms_v1`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("chester_queries.first_ms: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("migrate: chester_queries.first_ms can now be empty")
	return nil
}

// once runs fn if the meta key is absent, then records it.
func (s *Store) once(key string, fn func() error) error {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v); err == nil {
		return nil
	}
	if err := fn(); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	_, err := s.db.Exec(`INSERT INTO meta (key,value) VALUES (?,?)`, key, utcString(time.Now()))
	return err
}

// backfillPins pins every pre-existing post that has none: the photo's EXIF
// GPS, else the chosen checkpoint, else the first checkpoint of the post's
// leg — which is then recorded as the post's checkpoint, so the map popup can
// name it. Hidden posts stay unpinned. Once per volume; later posts resolve
// their pin at post time and never come back through here.
func (s *Store) backfillPins() error {
	rows, err := s.db.Query(`SELECT p.id, p.leg_id, COALESCE(p.checkpoint_id,0), ph.lat, ph.lng
	  FROM posts p LEFT JOIN photos ph ON ph.id=p.photo_id
	  WHERE p.hide_location=0 AND p.pin_source='none' AND p.lat IS NULL ORDER BY p.id`)
	if err != nil {
		return err
	}
	type row struct {
		id, legID, cpID int64
		phLat, phLng    sql.NullFloat64
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.legID, &r.cpID, &r.phLat, &r.phLng); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range todo {
		if r.phLat.Valid && r.phLng.Valid {
			if _, err := s.db.Exec(`UPDATE posts SET lat=?,lng=?,pin_source='exif' WHERE id=?`,
				r.phLat.Float64, r.phLng.Float64, r.id); err != nil {
				return err
			}
			log.Printf("migrate: post %d pinned from the photo's EXIF GPS", r.id)
			continue
		}
		var cp *Checkpoint
		if r.cpID != 0 {
			cp, _ = s.Checkpoint(r.cpID)
		}
		if cp == nil {
			cps, err := s.CheckpointsByLeg(r.legID)
			if err != nil {
				return err
			}
			if len(cps) > 0 {
				cp = &cps[0]
			}
		}
		if cp == nil {
			log.Printf("migrate: post %d has no photo GPS, checkpoint or leg checkpoint — left unpinned", r.id)
			continue
		}
		// cp came through scanCheckpoint, so a fuzzed point is already snapped:
		// the private coordinate never lands in the posts row.
		if _, err := s.db.Exec(`UPDATE posts SET lat=?,lng=?,pin_source='checkpoint',
		  checkpoint_id=COALESCE(checkpoint_id,?) WHERE id=?`, cp.Lat, cp.Lng, cp.ID, r.id); err != nil {
			return err
		}
		log.Printf("migrate: post %d pinned near checkpoint %d %q (%s)", r.id, cp.Ord, cp.Name, cp.Precision)
	}
	log.Printf("migrate: pin backfill done, %d unpinned post(s) examined", len(todo))
	return nil
}

// fuzzPinsNearPrivate applies the private-place rule (nearPrivate) to posts
// pinned before it existed: a raw EXIF or browser pin at a fuzzed checkpoint
// is replaced by that checkpoint's snapped point, the pin becomes a checkpoint
// pin, and the phone's accuracy radius is dropped. The photo row's own GPS is
// left alone — nothing renders it. Once per volume, one log line per post.
func (s *Store) fuzzPinsNearPrivate() error {
	rows, err := s.db.Query(`SELECT p.id, p.lat, p.lng, p.pin_source, l.ride_slug
	  FROM posts p JOIN legs l ON l.id=p.leg_id
	  WHERE p.pin_source IN ('exif','browser') AND p.lat IS NOT NULL AND p.lng IS NOT NULL
	  ORDER BY p.id`)
	if err != nil {
		return err
	}
	type row struct {
		id        int64
		lat, lng  float64
		src, ride string
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.lat, &r.lng, &r.src, &r.ride); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	cpsByRide := map[string][]Checkpoint{}
	snapped := 0
	for _, r := range todo {
		cps, ok := cpsByRide[r.ride]
		if !ok {
			legs, err := s.Legs(r.ride)
			if err != nil {
				return err
			}
			cps = rideCheckpoints(legs)
			cpsByRide[r.ride] = cps
		}
		cp := nearPrivate(r.lat, r.lng, cps)
		if cp == nil {
			continue
		}
		// cp came through scanCheckpoint, so it is already snapped.
		if _, err := s.db.Exec(`UPDATE posts SET lat=?,lng=?,pin_source='checkpoint',accuracy_m=NULL,
		  checkpoint_id=? WHERE id=?`, cp.Lat, cp.Lng, cp.ID, r.id); err != nil {
			return err
		}
		snapped++
		log.Printf("migrate: post %d %s pin was at a private place — snapped to checkpoint %d %q", r.id, r.src, cp.Ord, cp.Name)
	}
	log.Printf("migrate: private-place pin sweep done, %d of %d raw pin(s) snapped", snapped, len(todo))
	return nil
}

func (s *Store) rideCount() (n int) {
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM rides`).Scan(&n)
	return
}

// --- time helpers ----------------------------------------------------------

const utcLayout = time.RFC3339

func utcString(t time.Time) string { return t.UTC().Format(utcLayout) }

func parseUTC(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(utcLayout, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// loadLoc never fails the page: an unknown zone renders in UTC and says so.
func loadLoc(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// --- fuzzing -----------------------------------------------------------------

// fuzzGrid is the snap size for private points: 0.05° ≈ 5.5 km of latitude.
const fuzzGrid = 0.05

func snap(v float64) float64 {
	// Round the product too, or 30.4 comes back as 30.400000000000002.
	return math.Round(math.Round(v/fuzzGrid)*fuzzGrid*1000) / 1000
}

func (c *Checkpoint) fuzz() {
	if c.Precision == precisionFuzzed {
		c.Lat, c.Lng = snap(c.Lat), snap(c.Lng)
	}
}

// privateRadiusKm: a raw pin this close to a fuzzed checkpoint IS the private
// place. A browser fix from the driveway at departure, or a library photo's
// EXIF from the Cloudcroft street, would otherwise draw the home on the
// public map at 11 m precision.
const privateRadiusKm = 2.0

// haversineKm is the great-circle distance in kilometres.
func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

// nearPrivate returns the fuzzed checkpoint a raw (EXIF or browser) pin
// belongs to, or nil. The checkpoints must already be snapped — anything
// from scanCheckpoint is. A pin belongs to a fuzzed checkpoint when it snaps
// into the same 0.05° cell (the cell IS the fuzz, and a home need not sit at
// its centre: the half-diagonal is ~3.7 km at this latitude) or when it lies
// within privateRadiusKm of the cell point, which catches a driveway just
// over a cell edge. The nearest wins if two qualify.
func nearPrivate(lat, lng float64, cps []Checkpoint) *Checkpoint {
	var best *Checkpoint
	bestKm := math.Inf(1)
	for i := range cps {
		c := &cps[i]
		if c.Precision != precisionFuzzed {
			continue
		}
		km := haversineKm(lat, lng, c.Lat, c.Lng)
		sameCell := snap(lat) == c.Lat && snap(lng) == c.Lng
		if !sameCell && km >= privateRadiusKm {
			continue
		}
		if km < bestKm {
			best, bestKm = c, km
		}
	}
	return best
}

// publishPosition is the fuzz seam for a position that is about to leave the
// site by a road other than a post: the en-route helper sends one to an API.
// A point that belongs to a private place is replaced by that place's snapped
// point, exactly as a post's pin is (nearPrivate), and the test is widened to
// the line mask's radius, because this point goes to a third party and a
// vertex of a leg line is held to that radius already. Anywhere else the
// point is published to three decimals, about a hundred metres: enough to
// find the next fuel stop, and no finer than that needs. The checkpoints
// must be the store's own, already snapped.
func publishPosition(lat, lng float64, cps []Checkpoint) (LatLng, *Checkpoint) {
	return publishPositionAt(lat, lng, cps, false)
}

// publishPostPosition is publishPosition for a signed-in driver's post: the
// same private tests run first, then the point goes at the precision it is
// stored (a fuzzed post is stored fuzzy already). Ruled 2026-11-08.
func publishPostPosition(lat, lng float64, cps []Checkpoint) (LatLng, *Checkpoint) {
	return publishPositionAt(lat, lng, cps, true)
}

func publishPositionAt(lat, lng float64, cps []Checkpoint, exact bool) (LatLng, *Checkpoint) {
	if pc := nearPrivate(lat, lng, cps); pc != nil {
		return LatLng{Lat: pc.Lat, Lng: pc.Lng}, pc
	}
	var best *Checkpoint
	bestKm := math.Inf(1)
	for i := range cps {
		c := &cps[i]
		if c.Precision != precisionFuzzed || !inPrivateZone(lat, lng, *c) {
			continue
		}
		if km := haversineKm(lat, lng, c.Lat, c.Lng); km < bestKm {
			best, bestKm = c, km
		}
	}
	if best != nil {
		return LatLng{Lat: best.Lat, Lng: best.Lng}, best
	}
	if exact {
		return LatLng{Lat: lat, Lng: lng}, nil
	}
	return LatLng{Lat: math.Round(lat*1000) / 1000, Lng: math.Round(lng*1000) / 1000}, nil
}

// rideCheckpoints flattens a ride's legs into one checkpoint slice.
func rideCheckpoints(legs []Leg) []Checkpoint {
	var out []Checkpoint
	for _, l := range legs {
		out = append(out, l.Checkpoints...)
	}
	return out
}

// lineMaskKm is how far around a private place a polyline is not drawn. The
// snapped point is the centre of a 0.05° cell and the real place can sit in
// its corner, about 3.7 km out, so a radius the size of the cell would still
// let a line walk up to the door from the far side. 8 km leaves every drawn
// vertex at least ~4 km from anywhere the real place can be.
const lineMaskKm = 8.0

// fuzzedOf picks the private places out of a checkpoint list. The list must
// have come through scanCheckpoint or Checkpoint.fuzz, so each is already its
// snapped point.
func fuzzedOf(cps []Checkpoint) []Checkpoint {
	var out []Checkpoint
	for _, c := range cps {
		if c.Precision == precisionFuzzed {
			out = append(out, c)
		}
	}
	return out
}

// inPrivateZone reports whether a raw vertex is too close to a private place
// to be drawn: in its cell, or within lineMaskKm of the cell point.
func inPrivateZone(lat, lng float64, c Checkpoint) bool {
	if snap(lat) == c.Lat && snap(lng) == c.Lng {
		return true
	}
	return haversineKm(lat, lng, c.Lat, c.Lng) < lineMaskKm
}

// maskLine is the polyline half of the fuzz seam. Every run of vertices
// inside a private place's zone collapses to that place's snapped point, so
// the line still reaches the pin (no gap on the map) and never draws the road
// to the door. A routed line is the worst case: its last vertices ARE the
// driveway. A line that cannot be read comes back empty rather than raw.
func maskLine(line json.RawMessage, fuzzed []Checkpoint) json.RawMessage {
	if len(fuzzed) == 0 {
		return line
	}
	var pts [][]float64
	if err := json.Unmarshal(line, &pts); err != nil {
		return json.RawMessage("[]")
	}
	out := make([][]float64, 0, len(pts))
	run := -1 // the private place the current run of masked vertices belongs to
	for _, p := range pts {
		if len(p) < 2 {
			continue
		}
		hit := -1
		for i := range fuzzed {
			if inPrivateZone(p[1], p[0], fuzzed[i]) {
				hit = i
				break
			}
		}
		if hit < 0 {
			out = append(out, []float64{p[0], p[1]})
			run = -1
			continue
		}
		if hit != run {
			out = append(out, []float64{fuzzed[hit].Lng, fuzzed[hit].Lat})
			run = hit
		}
	}
	// One point is a pin, not a line; say it twice so it stays a LineString.
	if len(out) == 1 {
		out = append(out, out[0])
	}
	b, err := json.Marshal(out)
	if err != nil {
		return json.RawMessage("[]")
	}
	return b
}

// --- rides / legs / checkpoints / sights -------------------------------------

func (s *Store) Ride(slug string) (*Ride, error) {
	var r Ride
	err := s.db.QueryRow(`SELECT slug,name,start_date,end_date,mode,origin_label,terminus_label
	  FROM rides WHERE slug=?`, slug).
		Scan(&r.Slug, &r.Name, &r.StartDate, &r.EndDate, &r.Mode, &r.OriginLabel, &r.TerminusLabel)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

const legCols = `id,ride_slug,ord,slug,name,date,est_miles,est_hours,route_hint,line_geojson`

func scanLeg(sc interface{ Scan(...any) error }) (Leg, error) {
	var l Leg
	var line string
	err := sc.Scan(&l.ID, &l.RideSlug, &l.Ord, &l.Slug, &l.Name, &l.Date,
		&l.EstMiles, &l.EstHours, &l.RouteHint, &line)
	if err != nil {
		return l, err
	}
	var g struct {
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if json.Unmarshal([]byte(line), &g) == nil && len(g.Coordinates) > 0 {
		l.Line = g.Coordinates
	} else {
		l.Line = json.RawMessage("[]")
	}
	return l, nil
}

// Legs returns a ride's legs in order, each with its checkpoints and sights
// attached and its TZ set from its first checkpoint.
func (s *Store) Legs(rideSlug string) ([]Leg, error) {
	rows, err := s.db.Query(`SELECT `+legCols+` FROM legs WHERE ride_slug=? ORDER BY ord`, rideSlug)
	if err != nil {
		return nil, err
	}
	var out []Leg
	for rows.Next() {
		l, err := scanLeg(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		cps, err := s.CheckpointsByLeg(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Checkpoints = cps
		if len(cps) > 0 {
			out[i].TZ = cps[0].TZ
		}
		sights, err := s.SightsByLeg(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Sights = sights
	}
	// The polylines leave through the same seam the checkpoints do: a vertex
	// inside a private place's zone is replaced by that place's snapped point,
	// so a leg line cannot hand back the coordinate the checkpoint fuzz just
	// removed. Done here, over the whole ride, because a leg's origin is a
	// checkpoint of the leg before it.
	fuzzed := fuzzedOf(rideCheckpoints(out))
	for i := range out {
		out[i].Line = maskLine(out[i].Line, fuzzed)
	}
	return out, nil
}

// LegBySlug reads one leg THROUGH Legs, not beside it: the line mask needs the
// whole ride's private places, and a second read path is a second place to
// forget it.
func (s *Store) LegBySlug(rideSlug, slug string) (*Leg, error) {
	legs, err := s.Legs(rideSlug)
	if err != nil {
		return nil, err
	}
	for i := range legs {
		if legs[i].Slug == slug {
			return &legs[i], nil
		}
	}
	return nil, sql.ErrNoRows
}

const cpCols = `id,leg_id,ord,name,label,lat,lng,tz,precision,is_terminal,planned_arrive,planned_depart,arrived_at,skipped_at,skip_reason`

func scanCheckpoint(sc interface{ Scan(...any) error }) (Checkpoint, error) {
	var c Checkpoint
	err := sc.Scan(&c.ID, &c.LegID, &c.Ord, &c.Name, &c.Label, &c.Lat, &c.Lng, &c.TZ,
		&c.Precision, &c.IsTerminal, &c.PlannedArrive, &c.PlannedDepart, &c.ArrivedAt, &c.SkippedAt, &c.SkipReason)
	c.fuzz()
	return c, err
}

func (s *Store) CheckpointsByLeg(legID int64) ([]Checkpoint, error) {
	rows, err := s.db.Query(`SELECT `+cpCols+` FROM checkpoints WHERE leg_id=? ORDER BY ord`, legID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Checkpoint
	for rows.Next() {
		c, err := scanCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Checkpoint(id int64) (*Checkpoint, error) {
	c, err := scanCheckpoint(s.db.QueryRow(`SELECT `+cpCols+` FROM checkpoints WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// SetArrived stamps (or clears, with zero time) a checkpoint's arrival, and —
// when loc is given — records where the car actually got to.
//
// The plan puts a checkpoint at a town centroid; arriving is the moment the
// site learns the real place, so a location overwrites lat/lng and the
// checkpoint stops being a guess (precision becomes exact). A FUZZED
// checkpoint keeps its precision and is snapped before the write: the whole
// point of that flag is that the raw fix at a private place is never written
// down, and an arrival must not be the one path that writes it.
//
// loc nil stamps the time alone, which is what the one-tap button does. A
// clear (zero time) never touches the location: the stamp can be a mis-tap,
// but where the car stopped is a fact of its own.
func (s *Store) SetArrived(id int64, at time.Time, loc *LatLng) error {
	v := ""
	if !at.IsZero() {
		v = utcString(at)
	}
	if loc == nil {
		if at.IsZero() {
			// Undo: a stop taken back is neither arrived at nor skipped.
			_, err := s.db.Exec(`UPDATE checkpoints SET arrived_at='', skipped_at='', skip_reason='' WHERE id=?`, id)
			return err
		}
		_, err := s.db.Exec(`UPDATE checkpoints SET arrived_at=? WHERE id=?`, v, id)
		return err
	}
	var precision string
	if err := s.db.QueryRow(`SELECT precision FROM checkpoints WHERE id=?`, id).Scan(&precision); err != nil {
		return err
	}
	lat, lng := loc.Lat, loc.Lng
	if precision == precisionFuzzed {
		lat, lng = snap(lat), snap(lng)
	} else {
		precision = precisionExact
	}
	_, err := s.db.Exec(`UPDATE checkpoints SET arrived_at=?,lat=?,lng=?,precision=? WHERE id=?`,
		v, lat, lng, precision, id)
	return err
}

// SetSkipped marks a stop passed without a check-in. reason is "missed" or
// "rerouted". It never touches arrived_at.
func (s *Store) SetSkipped(id int64, at time.Time, reason string) error {
	_, err := s.db.Exec(`UPDATE checkpoints SET skipped_at=?,skip_reason=? WHERE id=?`, utcString(at), reason, id)
	return err
}

func (s *Store) SightsByLeg(legID int64) ([]Sight, error) {
	rows, err := s.db.Query(`SELECT id,leg_id,name,why,lat,lng,kind FROM sights WHERE leg_id=? ORDER BY id`, legID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sight
	for rows.Next() {
		var x Sight
		if err := rows.Scan(&x.ID, &x.LegID, &x.Name, &x.Why, &x.Lat, &x.Lng, &x.Kind); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// --- photos --------------------------------------------------------------------

func (s *Store) PhotoBySHA(sha string) (*Photo, error) {
	var p Photo
	err := s.db.QueryRow(`SELECT id,path,thumb_path,taken_at,tz,lat,lng,width,height,sha256
	  FROM photos WHERE sha256=?`, sha).
		Scan(&p.ID, &p.Path, &p.ThumbPath, &p.TakenAt, &p.TZ, &p.Lat, &p.Lng, &p.Width, &p.Height, &p.SHA256)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// PhotoPaths lists the media-relative path of every stored original, oldest
// first. It exists for the one-shot EXIF sweep (exif_strip.go), which has to
// visit files rather than rows.
func (s *Store) PhotoPaths() ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM photos WHERE path <> '' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreatePhoto(p *Photo) error {
	res, err := s.db.Exec(`INSERT INTO photos (path,thumb_path,taken_at,tz,lat,lng,width,height,sha256)
	  VALUES (?,?,?,?,?,?,?,?,?)`,
		p.Path, p.ThumbPath, p.TakenAt, p.TZ, p.Lat, p.Lng, p.Width, p.Height, p.SHA256)
	if err != nil {
		return err
	}
	p.ID, err = res.LastInsertId()
	return err
}

// --- posts ----------------------------------------------------------------------

func (s *Store) CreatePost(p *Post) error {
	var cp, ph any
	if p.CheckpointID != 0 {
		cp = p.CheckpointID
	}
	if p.PhotoID != 0 {
		ph = p.PhotoID
	}
	if p.CreatedAt == "" {
		p.CreatedAt = utcString(time.Now())
	}
	if p.PinSource == "" {
		p.PinSource = "none"
	}
	res, err := s.db.Exec(`INSERT INTO posts
	  (leg_id,checkpoint_id,kind,occurred_at,tz,title,body,photo_id,hide_location,created_at,
	   lat,lng,pin_source,accuracy_m,pin_fuzzed,exact_lat,exact_lng,exact_source)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.LegID, cp, p.Kind, p.OccurredAt, p.TZ, p.Title, p.Body, ph, p.HideLocation, p.CreatedAt,
		p.Lat, p.Lng, p.PinSource, p.AccuracyM, p.PinFuzzed, p.exactLat, p.exactLng, p.exactSource)
	if err != nil {
		return err
	}
	p.ID, err = res.LastInsertId()
	return err
}

func (s *Store) DeletePost(id int64) error {
	_, err := s.db.Exec(`DELETE FROM posts WHERE id=?`, id)
	return err
}

// UpdatePostMeta re-writes the editable metadata of an existing post — the
// edit screen (App.postEditSave). It touches NEITHER the photo nor the pin
// columns: a photo is content, and the pin has its own control
// (App.postRepin), so a metadata edit that silently re-derived a pin would
// undo a deliberate re-pin. When moving a leg or checkpoint does have to carry
// a checkpoint-derived pin with it, the handler says so explicitly with a
// second UpdatePostPin call.
//
// checkpointID 0 clears the association.
func (s *Store) UpdatePostMeta(id int64, p *Post) error {
	var cp any
	if p.CheckpointID != 0 {
		cp = p.CheckpointID
	}
	_, err := s.db.Exec(`UPDATE posts SET leg_id=?,checkpoint_id=?,kind=?,occurred_at=?,tz=?,
	  title=?,body=? WHERE id=?`,
		p.LegID, cp, p.Kind, p.OccurredAt, p.TZ, p.Title, p.Body, id)
	return err
}

// UpdatePostPin re-writes just the pin columns of an existing post — the
// re-pin control on an admin post card (App.postRepin). checkpointID 0 clears
// the association; anything else sets it, because fuzzing a point into a
// private checkpoint's cell moves the post to that checkpoint.
func (s *Store) UpdatePostPin(id int64, p *Post) error {
	var cp any
	if p.CheckpointID != 0 {
		cp = p.CheckpointID
	}
	// A re-pin starts clean: the kept exact point is whatever this pin says,
	// which is nothing unless it was itself fuzzed.
	_, err := s.db.Exec(`UPDATE posts SET lat=?,lng=?,pin_source=?,accuracy_m=?,pin_fuzzed=?,
	  checkpoint_id=?,hide_location=?,exact_lat=?,exact_lng=?,exact_source=? WHERE id=?`,
		p.Lat, p.Lng, p.PinSource, p.AccuracyM, p.PinFuzzed, cp, p.HideLocation,
		p.exactLat, p.exactLng, p.exactSource, id)
	return err
}

// FuzzPost is Fuzz This on an existing post: the current pin moves into the
// private exact_* columns and the post is published at the fuzzed point. It
// does nothing to a post with no pin or one already fuzzed (the kept point is
// then the true one, and must not be overwritten by a fuzzy one). source is
// the pin source the fuzzed point carries; checkpointID 0 leaves the post's
// checkpoint alone. Returns whether a post changed. The exact point is moved
// inside SQL and never passes through Go.
func (s *Store) FuzzPost(id int64, lat, lng float64, source string, checkpointID int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE posts SET exact_lat=lat,exact_lng=lng,exact_source=pin_source,
	  lat=?,lng=?,pin_source=?,accuracy_m=NULL,pin_fuzzed=1,
	  checkpoint_id=CASE WHEN ?=0 THEN checkpoint_id ELSE ? END
	  WHERE id=? AND pin_fuzzed=0 AND lat IS NOT NULL AND lng IS NOT NULL`,
		lat, lng, source, checkpointID, checkpointID, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UnfuzzPost is Unfuzz: the kept exact point becomes the pin again and the
// private columns are cleared. This is the only place the exact point is read,
// and it is read by the database itself: it never reaches Go, a template, a
// log or a JSON. (A fuzz that moved the post into a private checkpoint's cell
// also filed it under that checkpoint; Unfuzz leaves the filing alone.)
func (s *Store) UnfuzzPost(id int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE posts SET lat=exact_lat,lng=exact_lng,pin_source=exact_source,
	  pin_fuzzed=0,exact_lat=NULL,exact_lng=NULL,exact_source=''
	  WHERE id=? AND exact_lat IS NOT NULL AND exact_lng IS NOT NULL`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// CanUnfuzz says whether a kept exact point exists for the post. It reads no
// coordinate.
func (s *Store) CanUnfuzz(id int64) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM posts WHERE id=? AND exact_lat IS NOT NULL AND exact_lng IS NOT NULL`, id).Scan(&n)
	return n > 0
}

const postSelect = `SELECT p.id,p.leg_id,COALESCE(p.checkpoint_id,0),p.kind,p.occurred_at,p.tz,
  p.title,p.body,COALESCE(p.photo_id,0),p.hide_location,p.created_at,
  l.slug,l.name,l.ord,COALESCE(c.name,''),
  p.lat,p.lng,p.pin_source,p.accuracy_m,p.pin_fuzzed,
  ph.path,ph.thumb_path,ph.taken_at,ph.tz,ph.lat,ph.lng,ph.width,ph.height,ph.sha256
  FROM posts p
  JOIN legs l ON l.id=p.leg_id
  LEFT JOIN checkpoints c ON c.id=p.checkpoint_id
  LEFT JOIN photos ph ON ph.id=p.photo_id `

func scanPost(sc interface{ Scan(...any) error }) (Post, error) {
	var p Post
	var pLat, pLng, pAcc sql.NullFloat64
	var phPath, phThumb, phTaken, phTZ, phSHA sql.NullString
	var phLat, phLng sql.NullFloat64
	var phW, phH sql.NullInt64
	err := sc.Scan(&p.ID, &p.LegID, &p.CheckpointID, &p.Kind, &p.OccurredAt, &p.TZ,
		&p.Title, &p.Body, &p.PhotoID, &p.HideLocation, &p.CreatedAt,
		&p.LegSlug, &p.LegName, &p.LegOrd, &p.CheckpointName,
		&pLat, &pLng, &p.PinSource, &pAcc, &p.PinFuzzed,
		&phPath, &phThumb, &phTaken, &phTZ, &phLat, &phLng, &phW, &phH, &phSHA)
	if err != nil {
		return p, err
	}
	if p.PhotoID != 0 && phPath.Valid {
		ph := &Photo{ID: p.PhotoID, Path: phPath.String, ThumbPath: phThumb.String,
			TakenAt: phTaken.String, TZ: phTZ.String, Width: int(phW.Int64), Height: int(phH.Int64),
			SHA256: phSHA.String}
		if phLat.Valid && phLng.Valid {
			la, ln := phLat.Float64, phLng.Float64
			ph.Lat, ph.Lng = &la, &ln
		}
		p.Photo = ph
	}
	if pLat.Valid && pLng.Valid {
		la, ln := pLat.Float64, pLng.Float64
		p.Lat, p.Lng = &la, &ln
	}
	if pAcc.Valid {
		a := pAcc.Float64
		p.AccuracyM = &a
	}
	// Pin: whatever was resolved when the post was made (exif > browser >
	// checkpoint), unless the post hides its location — then no pin at all;
	// the post still shows on the timeline, just not on the map.
	if !p.HideLocation && p.PinSource != "none" && p.Lat != nil {
		p.PinLat, p.PinLng = p.Lat, p.Lng
	}
	return p, nil
}

// Posts returns a ride's posts newest-first. legID 0 = all legs.
func (s *Store) Posts(rideSlug string, legID int64) ([]Post, error) {
	q := postSelect + ` WHERE l.ride_slug=?`
	args := []any{rideSlug}
	if legID != 0 {
		q += ` AND p.leg_id=?`
		args = append(args, legID)
	}
	rows, err := s.db.Query(q+` ORDER BY p.occurred_at DESC, p.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Post(id int64) (*Post, error) {
	p, err := scanPost(s.db.QueryRow(postSelect+` WHERE p.id=?`, id))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// --- seeding -----------------------------------------------------------------------

// seedFile is the RWGPS-import shape: one LineString feature per leg (in
// order) and Point features per checkpoint (kind=checkpoint) and candidate
// sight (kind=candidate). An optional top-level "ride" block names the ride;
// without it the ride is derived from the legs. Private text (addresses)
// is NOT a seed property — see private.go.
//
// A trip document is the same shape plus three optional top-level keys:
// "options" (groups of route choices), "avg_mph" and "long_day_hours". A
// feature that carries "option" and "choice" belongs to that choice only; a
// feature without them is part of every plan. See trip.go.
type seedFile struct {
	Name string `json:"name"`
	Ride *struct {
		Slug          string `json:"slug"`
		Name          string `json:"name"`
		StartDate     string `json:"start_date"`
		EndDate       string `json:"end_date"`
		Mode          string `json:"mode"`
		OriginLabel   string `json:"origin_label"`
		TerminusLabel string `json:"terminus_label"`
	} `json:"ride"`
	Options      []tripOption `json:"options"`
	AvgMPH       float64      `json:"avg_mph"`
	LongDayHours float64      `json:"long_day_hours"`
	Features     []struct {
		Properties map[string]any `json:"properties"`
		Geometry   struct {
			Type        string          `json:"type"`
			Coordinates json.RawMessage `json:"coordinates"`
		} `json:"geometry"`
	} `json:"features"`
}

// tripOption is one group of alternatives — "which road on day 2" — and
// tripChoice one alternative in it. Default names the choice a fresh plan
// takes; empty means the first.
type tripOption struct {
	ID      string       `json:"id"`
	Title   string       `json:"title"`
	Default string       `json:"default"`
	Choices []tripChoice `json:"choices"`
}

type tripChoice struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// Selection is which choice each option group takes, plus the day the ride
// starts. The start date lives here, not on each leg, so a departure that
// slips a day is one saved field: a leg that carries a "day" and no "date"
// is dated from it.
type Selection struct {
	StartDate string            `json:"start_date,omitempty"`
	Choices   map[string]string `json:"choices,omitempty"`
}

type seedLeg struct {
	Ord                int
	Day                int
	Slug, Name, Date   string
	EstMiles, EstHours float64
	RouteHint, Line    string
	Option, Choice     string
	// DateFixed: the feature carried its own date. Otherwise Date was derived
	// from the start date and the leg's day, and moves when the start does.
	DateFixed bool
}

type seedCP struct {
	LegOrd, Ord                  int
	Name, Label                  string
	Lat, Lng                     float64
	TZ, Precision                string
	IsTerminal                   bool
	PlannedArrive, PlannedDepart string
	Option, Choice               string
}

type seedPlace struct {
	Name     string
	Lat, Lng float64
}

type seedSight struct {
	LegOrd    int
	Name, Why string
	Lat, Lng  float64
	Kind      string
}

// seedData is a parsed seed: what the planning side of the database should
// look like under one selection.
type seedData struct {
	Ride        Ride
	Legs        []seedLeg
	Checkpoints []seedCP
	Sights      []seedSight
	Places      []seedPlace
	// Options is what the document offers; Selection is what this parse took,
	// fully resolved — every option answered and the start date filled in.
	Options      []tripOption
	Selection    Selection
	AvgMPH       float64
	LongDayHours float64
	// DocStartDate is the start date the document itself gives, whatever the
	// selection says. Once a ride has a saved selection that selection's start
	// date wins (Selection.StartDate is the one in force), and a caller that
	// re-applies a document compares the two to say so out loud.
	DocStartDate string
}

func pstr(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func pnum(m map[string]any, k string) float64 {
	if v, ok := m[k].(float64); ok {
		return v
	}
	return 0
}

func pbool(m map[string]any, k string) bool {
	v, _ := m[k].(bool)
	return v
}

// toUTC normalises a seed timestamp (RFC3339 with any offset) to UTC text.
func toUTC(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return ""
	}
	return utcString(t)
}

const dateLayout = "2006-01-02"

// round1 keeps one decimal: what a mileage or an hour count is worth.
func round1(v float64) float64 { return math.Round(v*10) / 10 }

// checkOptions validates the option groups of a document and returns each
// group's valid choice ids. A document with a malformed group is refused
// whole: a half-understood option is a plan nobody chose.
func checkOptions(opts []tripOption) (map[string]map[string]bool, error) {
	valid := map[string]map[string]bool{}
	for i := range opts {
		o := &opts[i]
		if o.ID == "" {
			return nil, fmt.Errorf("option %d has no id", i+1)
		}
		if valid[o.ID] != nil {
			return nil, fmt.Errorf("option %q is declared twice", o.ID)
		}
		if len(o.Choices) == 0 {
			return nil, fmt.Errorf("option %q has no choices", o.ID)
		}
		ids := map[string]bool{}
		for _, c := range o.Choices {
			if c.ID == "" {
				return nil, fmt.Errorf("option %q has a choice with no id", o.ID)
			}
			if ids[c.ID] {
				return nil, fmt.Errorf("option %q declares choice %q twice", o.ID, c.ID)
			}
			ids[c.ID] = true
		}
		if o.Default == "" {
			o.Default = o.Choices[0].ID
		}
		if !ids[o.Default] {
			return nil, fmt.Errorf("option %q: default %q is not one of its choices", o.ID, o.Default)
		}
		valid[o.ID] = ids
	}
	return valid, nil
}

func parseSeed(path, defaultSlug string) (*seedData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseSeedBytes(raw, defaultSlug, nil)
}

// parseSeedBytes reads a seed or trip document under one selection. sel nil
// (or an option it does not answer, or answers with a choice the document no
// longer has) takes the document's default for that option, and an empty
// start date takes the document's — so a stored selection survives a
// re-pushed document that dropped one of its choices.
//
// A selection's start date WINS over the document's, the same way its choices
// do: the trip-keeper moved the departure with POST /driver/plan, and a
// document pushed again afterwards must not quietly move it back. The
// document's own start is kept in DocStartDate so the push can report that it
// was not applied.
func parseSeedBytes(raw []byte, defaultSlug string, sel *Selection) (*seedData, error) {
	var sf seedFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, err
	}
	valid, err := checkOptions(sf.Options)
	if err != nil {
		return nil, err
	}
	sd := &seedData{Ride: Ride{Slug: defaultSlug, Name: sf.Name, Mode: "CAR"},
		Options: sf.Options, AvgMPH: sf.AvgMPH, LongDayHours: sf.LongDayHours,
		Selection: Selection{Choices: map[string]string{}}}
	if sf.AvgMPH < 0 || sf.LongDayHours < 0 {
		return nil, fmt.Errorf("avg_mph and long_day_hours cannot be negative")
	}
	for _, o := range sf.Options {
		c := o.Default
		if sel != nil && valid[o.ID][sel.Choices[o.ID]] {
			c = sel.Choices[o.ID]
		}
		sd.Selection.Choices[o.ID] = c
	}
	if sf.Ride != nil {
		if sf.Ride.Slug != "" {
			sd.Ride.Slug = sf.Ride.Slug
		}
		if sf.Ride.Name != "" {
			sd.Ride.Name = sf.Ride.Name
		}
		sd.Ride.StartDate, sd.Ride.EndDate = sf.Ride.StartDate, sf.Ride.EndDate
		if sf.Ride.Mode != "" {
			sd.Ride.Mode = sf.Ride.Mode
		}
		sd.Ride.OriginLabel, sd.Ride.TerminusLabel = sf.Ride.OriginLabel, sf.Ride.TerminusLabel
	}
	sd.DocStartDate = sd.Ride.StartDate
	if sel != nil && sel.StartDate != "" {
		sd.Ride.StartDate = sel.StartDate
	}
	var start time.Time
	if sd.Ride.StartDate != "" {
		if start, err = time.Parse(dateLayout, sd.Ride.StartDate); err != nil {
			return nil, fmt.Errorf("start date %q is not YYYY-MM-DD", sd.Ride.StartDate)
		}
	}
	// taken reports whether a feature is part of this plan, and refuses one
	// that names an option or choice the document never declared — a typo
	// there would otherwise drop a road off every plan in silence.
	taken := func(p map[string]any) (bool, string, string, error) {
		opt, ch := pstr(p, "option"), pstr(p, "choice")
		if opt == "" && ch == "" {
			return true, "", "", nil
		}
		if valid[opt] == nil {
			return false, "", "", fmt.Errorf("feature %q names option %q, which the document does not declare", pstr(p, "name"), opt)
		}
		if !valid[opt][ch] {
			return false, "", "", fmt.Errorf("feature %q names choice %q, which option %q does not have", pstr(p, "name"), ch, opt)
		}
		return sd.Selection.Choices[opt] == ch, opt, ch, nil
	}

	legOrds := map[int]bool{}
	derived := false
	firstFixed := "" // the earliest date a leg carries itself
	for _, f := range sf.Features {
		if f.Geometry.Type != "LineString" {
			continue
		}
		p := f.Properties
		in, opt, ch, err := taken(p)
		if err != nil {
			return nil, err
		}
		if !in {
			continue
		}
		ord := int(pnum(p, "leg"))
		if ord == 0 {
			ord = len(sd.Legs) + 1
		}
		if legOrds[ord] {
			return nil, fmt.Errorf("leg %d appears twice in one plan (two roads for one leg need an option and a choice each)", ord)
		}
		slug := pstr(p, "slug")
		if slug == "" {
			slug = "leg-" + strconv.Itoa(ord)
			if ch != "" {
				// Two alternatives for one leg must not share a slug: the slug is
				// how the store tells which road a leg row currently holds.
				slug += "-" + ch
			}
		}
		day := int(pnum(p, "day"))
		if day < 1 {
			day = ord
		}
		date := pstr(p, "date")
		fixed := date != ""
		if fixed && (firstFixed == "" || date < firstFixed) {
			firstFixed = date
		}
		if date == "" && !start.IsZero() {
			date = start.AddDate(0, 0, day-1).Format(dateLayout)
			derived = true
		}
		miles := pnum(p, "est_miles")
		hours := pnum(p, "est_drive_hours")
		if hours == 0 && sf.AvgMPH > 0 {
			hours = round1(miles / sf.AvgMPH)
		}
		line, _ := json.Marshal(map[string]any{"type": "LineString", "coordinates": f.Geometry.Coordinates})
		sd.Legs = append(sd.Legs, seedLeg{Ord: ord, Day: day, Slug: slug, Name: pstr(p, "name"), Date: date,
			EstMiles: miles, EstHours: hours,
			RouteHint: pstr(p, "route_hint"), Line: string(line), Option: opt, Choice: ch, DateFixed: fixed})
		legOrds[ord] = true
	}
	if len(sd.Legs) == 0 {
		return nil, fmt.Errorf("no LineString legs")
	}
	sort.SliceStable(sd.Legs, func(i, j int) bool { return sd.Legs[i].Ord < sd.Legs[j].Ord })
	slugs := map[string]bool{}
	for _, l := range sd.Legs {
		if slugs[l.Slug] {
			return nil, fmt.Errorf("leg slug %q appears twice in one plan", l.Slug)
		}
		slugs[l.Slug] = true
	}
	var firstCP, lastCP *seedCP
	firstDate, lastDate := "", ""
	for _, l := range sd.Legs {
		if l.Date == "" {
			continue
		}
		if firstDate == "" || l.Date < firstDate {
			firstDate = l.Date
		}
		if l.Date > lastDate {
			lastDate = l.Date
		}
	}
	cpOrds := map[int]bool{}
	for _, f := range sf.Features {
		if f.Geometry.Type != "Point" {
			continue
		}
		p := f.Properties
		in, opt, ch, err := taken(p)
		if err != nil {
			return nil, err
		}
		if !in {
			continue
		}
		var xy []float64
		if err := json.Unmarshal(f.Geometry.Coordinates, &xy); err != nil || len(xy) < 2 {
			continue
		}
		if pstr(p, "kind") == "place" {
			// A public town name along the route: belongs to no leg.
			if n := strings.TrimSpace(pstr(p, "name")); n != "" {
				sd.Places = append(sd.Places, seedPlace{Name: n, Lat: xy[1], Lng: xy[0]})
			}
			continue
		}
		legOrd := int(pnum(p, "leg"))
		if !legOrds[legOrd] {
			return nil, fmt.Errorf("seed point %q: unknown leg %v", pstr(p, "name"), p["leg"])
		}
		switch pstr(p, "kind") {
		case "checkpoint":
			c := seedCP{LegOrd: legOrd, Ord: int(pnum(p, "order")), Name: pstr(p, "name"),
				Label: pstr(p, "label"), Lat: xy[1], Lng: xy[0], TZ: pstr(p, "tz"),
				Precision: pstr(p, "precision"), IsTerminal: pbool(p, "is_terminal"),
				PlannedArrive: toUTC(pstr(p, "planned_arrive")), PlannedDepart: toUTC(pstr(p, "planned_depart")),
				Option: opt, Choice: ch}
			if c.Precision == "" {
				c.Precision = precisionApprox
			}
			if c.TZ == "" {
				return nil, fmt.Errorf("seed checkpoint %q: tz is required (per-checkpoint, never ride-wide)", c.Name)
			}
			if _, err := time.LoadLocation(c.TZ); err != nil {
				return nil, fmt.Errorf("seed checkpoint %q: tz %q is not an IANA zone", c.Name, c.TZ)
			}
			if cpOrds[c.Ord] {
				return nil, fmt.Errorf("checkpoint order %d appears twice in one plan", c.Ord)
			}
			cpOrds[c.Ord] = true
			sd.Checkpoints = append(sd.Checkpoints, c)
			cc := c
			if firstCP == nil || cc.Ord < firstCP.Ord {
				firstCP = &cc
			}
			if lastCP == nil || cc.Ord > lastCP.Ord {
				lastCP = &cc
			}
		case "candidate":
			kind := pstr(p, "sight_kind")
			if kind == "" {
				kind = "SIGHT"
			}
			sd.Sights = append(sd.Sights, seedSight{LegOrd: legOrd, Name: pstr(p, "name"), Why: pstr(p, "why"),
				Lat: xy[1], Lng: xy[0], Kind: kind})
		}
	}
	sort.SliceStable(sd.Checkpoints, func(i, j int) bool { return sd.Checkpoints[i].Ord < sd.Checkpoints[j].Ord })
	// Derive what the ride block did not say. A plan whose dates were derived
	// from the start date ends when its last leg does, whatever the block
	// says: a choice that adds a day moves the end.
	if sd.Ride.StartDate == "" {
		sd.Ride.StartDate = firstDate
	}
	if sd.Ride.EndDate == "" || derived {
		sd.Ride.EndDate = lastDate
	}
	if sd.Ride.OriginLabel == "" && firstCP != nil {
		sd.Ride.OriginLabel = strings.TrimSpace(firstCP.Name)
	}
	if sd.Ride.TerminusLabel == "" && lastCP != nil {
		sd.Ride.TerminusLabel = strings.TrimSpace(lastCP.Name)
	}
	sd.Selection.StartDate = sd.Ride.StartDate
	if sd.DocStartDate == "" {
		// No ride.start_date: the document starts on its earliest dated leg.
		sd.DocStartDate = firstFixed
	}
	return sd, nil
}

// Seed creates a ride from a seed file. created=false means the ride slug
// was already present and nothing was written; RefreshPlanning is then the
// path by which the seed reaches the database.
func (s *Store) Seed(path, defaultSlug string) (slug string, created bool, err error) {
	sd, err := parseSeed(path, defaultSlug)
	if err != nil {
		return "", false, fmt.Errorf("seed %s: %w", path, err)
	}
	if _, err := s.Ride(sd.Ride.Slug); err == nil {
		return sd.Ride.Slug, false, nil
	}
	return sd.Ride.Slug, true, s.seedPlan(sd, nil)
}

// seedPlan inserts a ride that is not in the database yet. after, when given,
// runs inside the same transaction — the selection that produced the plan is
// stored with it or not at all.
func (s *Store) seedPlan(sd *seedData, after func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r := sd.Ride
	if _, err := tx.Exec(`INSERT INTO rides (slug,name,start_date,end_date,mode,origin_label,terminus_label)
	  VALUES (?,?,?,?,?,?,?)`,
		r.Slug, r.Name, r.StartDate, r.EndDate, r.Mode, r.OriginLabel, r.TerminusLabel); err != nil {
		return fmt.Errorf("seed ride: %w", err)
	}
	legIDs := map[int]int64{}
	for _, l := range sd.Legs {
		res, err := tx.Exec(`INSERT INTO legs (ride_slug,ord,slug,name,date,est_miles,est_hours,route_hint,line_geojson)
		  VALUES (?,?,?,?,?,?,?,?,?)`,
			r.Slug, l.Ord, l.Slug, l.Name, l.Date, l.EstMiles, l.EstHours, l.RouteHint, l.Line)
		if err != nil {
			return fmt.Errorf("seed leg %d: %w", l.Ord, err)
		}
		legIDs[l.Ord], _ = res.LastInsertId()
	}
	for _, c := range sd.Checkpoints {
		if _, err := tx.Exec(`INSERT INTO checkpoints
		  (leg_id,ord,name,label,lat,lng,tz,precision,is_terminal,planned_arrive,planned_depart)
		  VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			legIDs[c.LegOrd], c.Ord, c.Name, c.Label, c.Lat, c.Lng, c.TZ, c.Precision, c.IsTerminal,
			c.PlannedArrive, c.PlannedDepart); err != nil {
			return fmt.Errorf("seed checkpoint %q: %w", c.Name, err)
		}
	}
	for _, x := range sd.Sights {
		if _, err := tx.Exec(`INSERT INTO sights (leg_id,name,why,lat,lng,kind) VALUES (?,?,?,?,?,?)`,
			legIDs[x.LegOrd], x.Name, x.Why, x.Lat, x.Lng, x.Kind); err != nil {
			return fmt.Errorf("seed sight %q: %w", x.Name, err)
		}
	}
	if err := replacePlaces(tx, r.Slug, sd.Places); err != nil {
		return err
	}
	if after != nil {
		if err := after(tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// replacePlaces makes a ride's places exactly the document's.
func replacePlaces(tx *sql.Tx, slug string, places []seedPlace) error {
	if _, err := tx.Exec(`DELETE FROM places WHERE ride_slug=?`, slug); err != nil {
		return err
	}
	for _, p := range places {
		if _, err := tx.Exec(`INSERT INTO places (ride_slug,name,lat,lng) VALUES (?,?,?,?)`,
			slug, p.Name, p.Lat, p.Lng); err != nil {
			return fmt.Errorf("place %q: %w", p.Name, err)
		}
	}
	return nil
}

// Places returns a ride's place names. Published to no page as a point: the
// hours bar reads them here and prints only a name.
func (s *Store) Places(slug string) ([]Place, error) {
	rows, err := s.db.Query(`SELECT name,lat,lng FROM places WHERE ride_slug=? ORDER BY id`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Place
	for rows.Next() {
		var p Place
		if err := rows.Scan(&p.Name, &p.Lat, &p.Lng); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RefreshCounts is the one-line boot summary of RefreshPlanning.
type RefreshCounts struct {
	Legs, LegsNew, CPs, CPsNew, CPsKept, Sights int
	// Pruned rows exist only for a trip with options: the legs and checkpoints
	// the plan no longer has after a choice changed.
	LegsPruned, CPsPruned int
}

func (c RefreshCounts) String() string {
	s := fmt.Sprintf("legs %d refreshed (%d new), checkpoints %d refreshed, %d kept (arrived), %d new, sights %d replaced",
		c.Legs, c.LegsNew, c.CPs, c.CPsKept, c.CPsNew, c.Sights)
	if c.LegsPruned > 0 || c.CPsPruned > 0 {
		s += fmt.Sprintf(", %d leg(s) and %d checkpoint(s) dropped with the choice that had them", c.LegsPruned, c.CPsPruned)
	}
	return s
}

// RefreshPlanning makes planning data seed-owned until actualized, the
// spec-ride-rwgps-import-contract rule. Against an existing ride:
//   - legs, matched by (ride, ord), are refreshed always — a leg has no
//     actualized flag;
//   - checkpoints, matched by (ride, ord), are refreshed ONLY while
//     arrived_at is empty; a stamped checkpoint is never touched;
//   - the candidate sight set is replaced;
//   - posts and photos are never touched.
//
// It runs on every boot, which is deploy-coupled work: a seed edit reaches
// the site on the next deploy and never on a suspend/resume.
func (s *Store) RefreshPlanning(path, defaultSlug string) (RefreshCounts, error) {
	sd, err := parseSeed(path, defaultSlug)
	if err != nil {
		return RefreshCounts{}, fmt.Errorf("seed %s: %w", path, err)
	}
	return s.refreshPlan(sd, false, nil)
}

// PlanConflictError is a plan change refused because it would reach something
// the road has already made real: an arrived checkpoint, or a leg or
// checkpoint that holds posts. Nothing was written.
type PlanConflictError struct{ Reasons []string }

func (e *PlanConflictError) Error() string {
	return "the plan cannot change that way: " + strings.Join(e.Reasons, "; ")
}

// planConflicts is the guard on a plan that can drop or replace rows — a trip
// with options. It compares what the database holds with what the new plan
// wants, and names every row the change would reach that is no longer the
// plan's to move:
//
//   - a leg that holds posts must stay, on the same road (same slug). Posts
//     cascade with their leg, so dropping it would delete them.
//   - a checkpoint that is arrived or holds posts must stay what it is: same
//     order, same name, same leg. A choice that adds a stop renumbers every
//     checkpoint after it, and an arrived checkpoint cannot be renumbered.
//
// Dates are not identity: moving the start date re-dates every leg and is
// never a conflict.
func planConflicts(tx *sql.Tx, sd *seedData) ([]string, error) {
	var reasons []string
	planLegs := map[int]seedLeg{}
	for _, l := range sd.Legs {
		planLegs[l.Ord] = l
	}
	planCPs := map[int]seedCP{}
	for _, c := range sd.Checkpoints {
		planCPs[c.Ord] = c
	}
	rows, err := tx.Query(`SELECT l.ord, l.slug, (SELECT COUNT(*) FROM posts p WHERE p.leg_id=l.id)
	  FROM legs l WHERE l.ride_slug=? ORDER BY l.ord`, sd.Ride.Slug)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ord, posts int
		var slug string
		if err := rows.Scan(&ord, &slug, &posts); err != nil {
			rows.Close()
			return nil, err
		}
		if posts == 0 {
			continue
		}
		pl, ok := planLegs[ord]
		switch {
		case !ok:
			reasons = append(reasons, fmt.Sprintf("leg %d holds %d post(s) and the new plan has no leg %d", ord, posts, ord))
		case pl.Slug != slug:
			reasons = append(reasons, fmt.Sprintf("leg %d holds %d post(s) and the new plan takes a different road for it", ord, posts))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = tx.Query(`SELECT c.ord, c.name, l.ord, c.arrived_at, c.skipped_at,
	    (SELECT COUNT(*) FROM posts p WHERE p.checkpoint_id=c.id)
	  FROM checkpoints c JOIN legs l ON l.id=c.leg_id WHERE l.ride_slug=? ORDER BY c.ord`, sd.Ride.Slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ord, legOrd, posts int
		var name, arrived, skipped string
		if err := rows.Scan(&ord, &name, &legOrd, &arrived, &skipped, &posts); err != nil {
			return nil, err
		}
		if arrived == "" && skipped == "" && posts == 0 {
			continue
		}
		why := "is arrived"
		if arrived == "" && skipped != "" {
			why = "is skipped"
		} else if arrived == "" {
			why = fmt.Sprintf("holds %d post(s)", posts)
		}
		pc, ok := planCPs[ord]
		switch {
		case !ok:
			reasons = append(reasons, fmt.Sprintf("checkpoint %d %s and the new plan has no checkpoint %d", ord, why, ord))
		case pc.Name != name || pc.LegOrd != legOrd:
			reasons = append(reasons, fmt.Sprintf("checkpoint %d %s and the new plan would put a different stop at %d", ord, why, ord))
		}
	}
	return reasons, rows.Err()
}

// refreshPlan writes a parsed plan over an existing ride. prune is the
// difference between a seed and a trip with options: a seed only ever adds
// and refreshes, while a changed choice has to take away the legs and
// checkpoints the plan no longer has. Pruning is guarded by planConflicts
// first, inside the same transaction, so it can only ever remove rows that
// are still pure plan. after runs in the transaction too.
func (s *Store) refreshPlan(sd *seedData, prune bool, after func(*sql.Tx) error) (RefreshCounts, error) {
	var n RefreshCounts
	r := sd.Ride
	if _, err := s.Ride(r.Slug); err != nil {
		return n, fmt.Errorf("ride %s not present", r.Slug)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return n, err
	}
	defer tx.Rollback()

	if prune {
		reasons, err := planConflicts(tx, sd)
		if err != nil {
			return n, err
		}
		if len(reasons) > 0 {
			return n, &PlanConflictError{Reasons: reasons}
		}
	}

	if _, err := tx.Exec(`UPDATE rides SET name=?,start_date=?,end_date=?,mode=?,origin_label=?,terminus_label=? WHERE slug=?`,
		r.Name, r.StartDate, r.EndDate, r.Mode, r.OriginLabel, r.TerminusLabel, r.Slug); err != nil {
		return n, err
	}

	if prune {
		// Legs first, and before the refresh: a leg row's slug is unique per
		// ride, and the row being dropped may hold the slug a kept row is about
		// to take. Its checkpoints go with it (cascade); the guard above has
		// already established none of them is arrived or holds a post.
		keepLegs := map[int]bool{}
		for _, l := range sd.Legs {
			keepLegs[l.Ord] = true
		}
		ords, err := queryInts(tx, `SELECT ord FROM legs WHERE ride_slug=?`, r.Slug)
		if err != nil {
			return n, err
		}
		for _, ord := range ords {
			if keepLegs[ord] {
				continue
			}
			var cps int
			_ = tx.QueryRow(`SELECT COUNT(*) FROM checkpoints c JOIN legs l ON l.id=c.leg_id
			  WHERE l.ride_slug=? AND l.ord=?`, r.Slug, ord).Scan(&cps)
			if _, err := tx.Exec(`DELETE FROM legs WHERE ride_slug=? AND ord=?`, r.Slug, ord); err != nil {
				return n, fmt.Errorf("drop leg %d: %w", ord, err)
			}
			n.LegsPruned++
			n.CPsPruned += cps
		}
		// Park the surviving slugs out of the way so two legs can trade roads
		// without tripping the unique index mid-update.
		if _, err := tx.Exec(`UPDATE legs SET slug='~'||ord WHERE ride_slug=?`, r.Slug); err != nil {
			return n, err
		}
	}

	legIDs := map[int]int64{}
	for _, l := range sd.Legs {
		res, err := tx.Exec(`UPDATE legs SET slug=?,name=?,date=?,est_miles=?,est_hours=?,route_hint=?,line_geojson=?
		  WHERE ride_slug=? AND ord=?`,
			l.Slug, l.Name, l.Date, l.EstMiles, l.EstHours, l.RouteHint, l.Line, r.Slug, l.Ord)
		if err != nil {
			return n, fmt.Errorf("refresh leg %d: %w", l.Ord, err)
		}
		if a, _ := res.RowsAffected(); a > 0 {
			n.Legs++
		} else {
			if _, err := tx.Exec(`INSERT INTO legs (ride_slug,ord,slug,name,date,est_miles,est_hours,route_hint,line_geojson)
			  VALUES (?,?,?,?,?,?,?,?,?)`,
				r.Slug, l.Ord, l.Slug, l.Name, l.Date, l.EstMiles, l.EstHours, l.RouteHint, l.Line); err != nil {
				return n, fmt.Errorf("refresh new leg %d: %w", l.Ord, err)
			}
			n.LegsNew++
		}
	}
	rows, err := tx.Query(`SELECT id,ord FROM legs WHERE ride_slug=?`, r.Slug)
	if err != nil {
		return n, err
	}
	for rows.Next() {
		var id int64
		var ord int
		if err := rows.Scan(&id, &ord); err != nil {
			rows.Close()
			return n, err
		}
		legIDs[ord] = id
	}
	rows.Close()

	for _, c := range sd.Checkpoints {
		res, err := tx.Exec(`UPDATE checkpoints SET leg_id=?,name=?,label=?,lat=?,lng=?,tz=?,precision=?,
		  is_terminal=?,planned_arrive=?,planned_depart=?
		  WHERE arrived_at='' AND skipped_at='' AND id IN (
		    SELECT c.id FROM checkpoints c JOIN legs l ON l.id=c.leg_id WHERE l.ride_slug=? AND c.ord=?)`,
			legIDs[c.LegOrd], c.Name, c.Label, c.Lat, c.Lng, c.TZ, c.Precision, c.IsTerminal,
			c.PlannedArrive, c.PlannedDepart, r.Slug, c.Ord)
		if err != nil {
			return n, fmt.Errorf("refresh checkpoint %d: %w", c.Ord, err)
		}
		if a, _ := res.RowsAffected(); a > 0 {
			n.CPs++
			continue
		}
		var have int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM checkpoints c JOIN legs l ON l.id=c.leg_id WHERE l.ride_slug=? AND c.ord=?`,
			r.Slug, c.Ord).Scan(&have)
		if have > 0 {
			n.CPsKept++ // actualized: arrived_at is set, the seed no longer owns it
			continue
		}
		if _, err := tx.Exec(`INSERT INTO checkpoints
		  (leg_id,ord,name,label,lat,lng,tz,precision,is_terminal,planned_arrive,planned_depart)
		  VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			legIDs[c.LegOrd], c.Ord, c.Name, c.Label, c.Lat, c.Lng, c.TZ, c.Precision, c.IsTerminal,
			c.PlannedArrive, c.PlannedDepart); err != nil {
			return n, fmt.Errorf("refresh new checkpoint %d: %w", c.Ord, err)
		}
		n.CPsNew++
	}

	if prune {
		keepCPs := map[int]bool{}
		for _, c := range sd.Checkpoints {
			keepCPs[c.Ord] = true
		}
		ords, err := queryInts(tx, `SELECT c.ord FROM checkpoints c JOIN legs l ON l.id=c.leg_id WHERE l.ride_slug=?`, r.Slug)
		if err != nil {
			return n, err
		}
		for _, ord := range ords {
			if keepCPs[ord] {
				continue
			}
			if _, err := tx.Exec(`DELETE FROM checkpoints WHERE id IN (
			    SELECT c.id FROM checkpoints c JOIN legs l ON l.id=c.leg_id WHERE l.ride_slug=? AND c.ord=?)`,
				r.Slug, ord); err != nil {
				return n, fmt.Errorf("drop checkpoint %d: %w", ord, err)
			}
			n.CPsPruned++
		}
	}

	if _, err := tx.Exec(`DELETE FROM sights WHERE leg_id IN (SELECT id FROM legs WHERE ride_slug=?)`, r.Slug); err != nil {
		return n, err
	}
	for _, x := range sd.Sights {
		if _, err := tx.Exec(`INSERT INTO sights (leg_id,name,why,lat,lng,kind) VALUES (?,?,?,?,?,?)`,
			legIDs[x.LegOrd], x.Name, x.Why, x.Lat, x.Lng, x.Kind); err != nil {
			return n, fmt.Errorf("refresh sight %q: %w", x.Name, err)
		}
		n.Sights++
	}
	if err := replacePlaces(tx, r.Slug, sd.Places); err != nil {
		return n, err
	}
	if after != nil {
		if err := after(tx); err != nil {
			return n, err
		}
	}
	return n, tx.Commit()
}

func queryInts(tx *sql.Tx, q string, args ...any) ([]int, error) {
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// --- meta: the active ride and each trip's stored selection ---------------------

// metaActiveRide names the ride the site shows. It is data, not config: the
// trip-keeper sets it with one authenticated call and it is read per request,
// so it takes effect on a machine that has not booted in months. Absent, the
// site shows RIDE_SLUG.
const metaActiveRide = "active_ride"

func metaSelectionKey(slug string) string { return "selection:" + slug }

// Meta reads one meta value; ok is false when the key is absent.
func (s *Store) Meta(key string) (string, bool) {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta (key,value) VALUES (?,?)
	  ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) DeleteMeta(key string) error {
	_, err := s.db.Exec(`DELETE FROM meta WHERE key=?`, key)
	return err
}

// StoredSelection is the selection last saved for a trip, or nil when none
// has been — in which case the document's own defaults stand.
func (s *Store) StoredSelection(slug string) *Selection {
	v, ok := s.Meta(metaSelectionKey(slug))
	if !ok {
		return nil
	}
	var sel Selection
	if json.Unmarshal([]byte(v), &sel) != nil {
		return nil
	}
	return &sel
}

// ApplyPlan is how a trip with options reaches the database: it creates the
// ride, or refreshes it under the guard, and records the selection that
// produced the plan in the same transaction. A refused change returns a
// *PlanConflictError and writes nothing.
func (s *Store) ApplyPlan(sd *seedData) (created bool, n RefreshCounts, err error) {
	selJSON, err := json.Marshal(sd.Selection)
	if err != nil {
		return false, n, err
	}
	saveSel := func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta (key,value) VALUES (?,?)
		  ON CONFLICT(key) DO UPDATE SET value=excluded.value`, metaSelectionKey(sd.Ride.Slug), string(selJSON))
		return err
	}
	if _, err := s.Ride(sd.Ride.Slug); err != nil {
		return true, n, s.seedPlan(sd, saveSel)
	}
	n, err = s.refreshPlan(sd, true, saveSel)
	return false, n, err
}

// PlanConflictsFor is the plan guard run read-only: the reasons ApplyPlan would
// refuse sd with, and nothing written. Nothing is changed by asking.
func (s *Store) PlanConflictsFor(sd *seedData) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return planConflicts(tx, sd)
}

// RideCensus is one ride's row counts: what is in the database for it,
// whichever ride the site is showing.
type RideCensus struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	Legs        int    `json:"legs"`
	Checkpoints int    `json:"checkpoints"`
	Arrived     int    `json:"arrived"`
	Posts       int    `json:"posts"`
}

func (s *Store) Rides() ([]RideCensus, error) {
	rows, err := s.db.Query(`SELECT r.slug, r.name, r.start_date, r.end_date,
	    (SELECT COUNT(*) FROM legs l WHERE l.ride_slug=r.slug),
	    (SELECT COUNT(*) FROM checkpoints c JOIN legs l ON l.id=c.leg_id WHERE l.ride_slug=r.slug),
	    (SELECT COUNT(*) FROM checkpoints c JOIN legs l ON l.id=c.leg_id WHERE l.ride_slug=r.slug AND c.arrived_at<>''),
	    (SELECT COUNT(*) FROM posts p JOIN legs l ON l.id=p.leg_id WHERE l.ride_slug=r.slug)
	  FROM rides r ORDER BY r.start_date, r.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RideCensus{}
	for rows.Next() {
		var c RideCensus
		if err := rows.Scan(&c.Slug, &c.Name, &c.StartDate, &c.EndDate, &c.Legs, &c.Checkpoints, &c.Arrived, &c.Posts); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// markCheckpoints sets every checkpoint's Mark, walking the ride in order.
// A ride of one leg is one day, so every point on it is a letter, the start
// and the finish included. On a ride of several days a terminal checkpoint
// carries its day as "d1", "d2"…; any other takes the next letter. A number
// always carries its unit. Call it wherever legs are loaded for a page.
func markCheckpoints(legs []Leg) {
	n := 0
	for i := range legs {
		for j := range legs[i].Checkpoints {
			c := &legs[i].Checkpoints[j]
			c.IsStart = i == 0 && j == 0
			if c.IsTerminal && len(legs) > 1 {
				c.Mark = "d" + strconv.Itoa(legs[i].Ord)
			} else {
				c.Mark = letterMark(n)
				n++
			}
		}
	}
}

// letterMark is A..Z, then AA, AB, … like a spreadsheet column.
func letterMark(n int) string {
	s := ""
	for n++; n > 0; n = (n - 1) / 26 {
		s = string(rune('A'+(n-1)%26)) + s
	}
	return s
}
