package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
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
  arrived_at     TEXT NOT NULL DEFAULT ''
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
	}); err != nil {
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

// rideCheckpoints flattens a ride's legs into one checkpoint slice.
func rideCheckpoints(legs []Leg) []Checkpoint {
	var out []Checkpoint
	for _, l := range legs {
		out = append(out, l.Checkpoints...)
	}
	return out
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
	return out, nil
}

func (s *Store) LegBySlug(rideSlug, slug string) (*Leg, error) {
	l, err := scanLeg(s.db.QueryRow(`SELECT `+legCols+` FROM legs WHERE ride_slug=? AND slug=?`, rideSlug, slug))
	if err != nil {
		return nil, err
	}
	if l.Checkpoints, err = s.CheckpointsByLeg(l.ID); err != nil {
		return nil, err
	}
	if len(l.Checkpoints) > 0 {
		l.TZ = l.Checkpoints[0].TZ
	}
	if l.Sights, err = s.SightsByLeg(l.ID); err != nil {
		return nil, err
	}
	return &l, nil
}

const cpCols = `id,leg_id,ord,name,label,lat,lng,tz,precision,is_terminal,planned_arrive,planned_depart,arrived_at`

func scanCheckpoint(sc interface{ Scan(...any) error }) (Checkpoint, error) {
	var c Checkpoint
	err := sc.Scan(&c.ID, &c.LegID, &c.Ord, &c.Name, &c.Label, &c.Lat, &c.Lng, &c.TZ,
		&c.Precision, &c.IsTerminal, &c.PlannedArrive, &c.PlannedDepart, &c.ArrivedAt)
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
	   lat,lng,pin_source,accuracy_m,pin_fuzzed)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.LegID, cp, p.Kind, p.OccurredAt, p.TZ, p.Title, p.Body, ph, p.HideLocation, p.CreatedAt,
		p.Lat, p.Lng, p.PinSource, p.AccuracyM, p.PinFuzzed)
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
	_, err := s.db.Exec(`UPDATE posts SET lat=?,lng=?,pin_source=?,accuracy_m=?,pin_fuzzed=?,
	  checkpoint_id=?,hide_location=? WHERE id=?`,
		p.Lat, p.Lng, p.PinSource, p.AccuracyM, p.PinFuzzed, cp, p.HideLocation, id)
	return err
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
	Features []struct {
		Properties map[string]any `json:"properties"`
		Geometry   struct {
			Type        string          `json:"type"`
			Coordinates json.RawMessage `json:"coordinates"`
		} `json:"geometry"`
	} `json:"features"`
}

type seedLeg struct {
	Ord                int
	Slug, Name, Date   string
	EstMiles, EstHours float64
	RouteHint, Line    string
}

type seedCP struct {
	LegOrd, Ord                  int
	Name, Label                  string
	Lat, Lng                     float64
	TZ, Precision                string
	IsTerminal                   bool
	PlannedArrive, PlannedDepart string
}

type seedSight struct {
	LegOrd    int
	Name, Why string
	Lat, Lng  float64
	Kind      string
}

// seedData is a parsed seed: what the planning side of the database should
// look like.
type seedData struct {
	Ride        Ride
	Legs        []seedLeg
	Checkpoints []seedCP
	Sights      []seedSight
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

func parseSeed(path, defaultSlug string) (*seedData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sf seedFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, err
	}
	sd := &seedData{Ride: Ride{Slug: defaultSlug, Name: sf.Name, Mode: "CAR"}}
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
	legOrds := map[int]bool{}
	for _, f := range sf.Features {
		if f.Geometry.Type != "LineString" {
			continue
		}
		p := f.Properties
		ord := int(pnum(p, "leg"))
		if ord == 0 {
			ord = len(sd.Legs) + 1
		}
		slug := pstr(p, "slug")
		if slug == "" {
			slug = "leg-" + strconv.Itoa(ord)
		}
		line, _ := json.Marshal(map[string]any{"type": "LineString", "coordinates": f.Geometry.Coordinates})
		sd.Legs = append(sd.Legs, seedLeg{Ord: ord, Slug: slug, Name: pstr(p, "name"), Date: pstr(p, "date"),
			EstMiles: pnum(p, "est_miles"), EstHours: pnum(p, "est_drive_hours"),
			RouteHint: pstr(p, "route_hint"), Line: string(line)})
		legOrds[ord] = true
	}
	if len(sd.Legs) == 0 {
		return nil, fmt.Errorf("seed %s: no LineString legs", path)
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
	for _, f := range sf.Features {
		if f.Geometry.Type != "Point" {
			continue
		}
		p := f.Properties
		var xy []float64
		if err := json.Unmarshal(f.Geometry.Coordinates, &xy); err != nil || len(xy) < 2 {
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
				PlannedArrive: toUTC(pstr(p, "planned_arrive")), PlannedDepart: toUTC(pstr(p, "planned_depart"))}
			if c.Precision == "" {
				c.Precision = precisionApprox
			}
			if c.TZ == "" {
				return nil, fmt.Errorf("seed checkpoint %q: tz is required (per-checkpoint, never ride-wide)", c.Name)
			}
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
	// Derive what the ride block did not say.
	if sd.Ride.StartDate == "" {
		sd.Ride.StartDate = firstDate
	}
	if sd.Ride.EndDate == "" {
		sd.Ride.EndDate = lastDate
	}
	if sd.Ride.OriginLabel == "" && firstCP != nil {
		sd.Ride.OriginLabel = strings.TrimSpace(firstCP.Name)
	}
	if sd.Ride.TerminusLabel == "" && lastCP != nil {
		sd.Ride.TerminusLabel = strings.TrimSpace(lastCP.Name)
	}
	return sd, nil
}

// Seed creates a ride from a seed file. created=false means the ride slug
// was already present and nothing was written; RefreshPlanning is then the
// path by which the seed reaches the database.
func (s *Store) Seed(path, defaultSlug string) (slug string, created bool, err error) {
	sd, err := parseSeed(path, defaultSlug)
	if err != nil {
		return "", false, err
	}
	if _, err := s.Ride(sd.Ride.Slug); err == nil {
		return sd.Ride.Slug, false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	r := sd.Ride
	if _, err := tx.Exec(`INSERT INTO rides (slug,name,start_date,end_date,mode,origin_label,terminus_label)
	  VALUES (?,?,?,?,?,?,?)`,
		r.Slug, r.Name, r.StartDate, r.EndDate, r.Mode, r.OriginLabel, r.TerminusLabel); err != nil {
		return "", false, fmt.Errorf("seed ride: %w", err)
	}
	legIDs := map[int]int64{}
	for _, l := range sd.Legs {
		res, err := tx.Exec(`INSERT INTO legs (ride_slug,ord,slug,name,date,est_miles,est_hours,route_hint,line_geojson)
		  VALUES (?,?,?,?,?,?,?,?,?)`,
			r.Slug, l.Ord, l.Slug, l.Name, l.Date, l.EstMiles, l.EstHours, l.RouteHint, l.Line)
		if err != nil {
			return "", false, fmt.Errorf("seed leg %d: %w", l.Ord, err)
		}
		legIDs[l.Ord], _ = res.LastInsertId()
	}
	for _, c := range sd.Checkpoints {
		if _, err := tx.Exec(`INSERT INTO checkpoints
		  (leg_id,ord,name,label,lat,lng,tz,precision,is_terminal,planned_arrive,planned_depart)
		  VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			legIDs[c.LegOrd], c.Ord, c.Name, c.Label, c.Lat, c.Lng, c.TZ, c.Precision, c.IsTerminal,
			c.PlannedArrive, c.PlannedDepart); err != nil {
			return "", false, fmt.Errorf("seed checkpoint %q: %w", c.Name, err)
		}
	}
	for _, x := range sd.Sights {
		if _, err := tx.Exec(`INSERT INTO sights (leg_id,name,why,lat,lng,kind) VALUES (?,?,?,?,?,?)`,
			legIDs[x.LegOrd], x.Name, x.Why, x.Lat, x.Lng, x.Kind); err != nil {
			return "", false, fmt.Errorf("seed sight %q: %w", x.Name, err)
		}
	}
	return r.Slug, true, tx.Commit()
}

// RefreshCounts is the one-line boot summary of RefreshPlanning.
type RefreshCounts struct {
	Legs, LegsNew, CPs, CPsNew, CPsKept, Sights int
}

func (c RefreshCounts) String() string {
	return fmt.Sprintf("legs %d refreshed (%d new), checkpoints %d refreshed, %d kept (arrived), %d new, sights %d replaced",
		c.Legs, c.LegsNew, c.CPs, c.CPsKept, c.CPsNew, c.Sights)
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
	var n RefreshCounts
	sd, err := parseSeed(path, defaultSlug)
	if err != nil {
		return n, err
	}
	r := sd.Ride
	if _, err := s.Ride(r.Slug); err != nil {
		return n, fmt.Errorf("ride %s not present", r.Slug)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return n, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE rides SET name=?,start_date=?,end_date=?,mode=?,origin_label=?,terminus_label=? WHERE slug=?`,
		r.Name, r.StartDate, r.EndDate, r.Mode, r.OriginLabel, r.TerminusLabel, r.Slug); err != nil {
		return n, err
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
		  WHERE arrived_at='' AND id IN (
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
	return n, tx.Commit()
}
