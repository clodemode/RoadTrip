package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Trip files on the volume.
//
// The repository is public and a trip is not: where it starts, where it ends
// and when. So a trip is not baked into the image the way seed/ is. It is one
// GeoJSON document at $DATA_DIR/trips/<slug>.geojson, pushed by the
// trip-keeper (POST /admin/trip), read at boot after the embedded seeds and
// applied the moment it is pushed — a suspended machine never re-runs boot.
//
// The document is the seed shape plus options (see seedFile). The database
// only ever holds ONE plan: the document under the stored selection. The
// alternatives stay in the file, and the planner page reads them from there
// through publishPlan, which is the store's fuzz seam applied to a document.

const tripsDir = "trips"

// maxTripBytes caps a pushed document. A routed week of driving is tens of
// kilobytes; the whole body is held in memory to be parsed.
const maxTripBytes = 4 << 20

var slugRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// validSlug is the whole defence against a slug that is really a path: it
// becomes a file name on the volume.
func validSlug(s string) bool { return len(s) > 0 && len(s) <= 64 && slugRE.MatchString(s) }

func tripPath(dataDir, slug string) string {
	return filepath.Join(dataDir, tripsDir, slug+".geojson")
}

// readTrip returns a trip document from the volume. The slug is validated
// here as well as at the door, so no caller can turn it into a path.
func readTrip(dataDir, slug string) ([]byte, error) {
	if !validSlug(slug) {
		return nil, fmt.Errorf("%q is not a trip slug", slug)
	}
	return os.ReadFile(tripPath(dataDir, slug))
}

// stageTrip writes a document beside its final name and hands back the two
// ways the write can end. commit is the rename — the one write a concurrent
// reader cannot catch half-done; discard removes the temp file. Mode 0600:
// this is the file the private places live in.
func stageTrip(dataDir, slug string, raw []byte) (commit func() error, discard func(), err error) {
	if !validSlug(slug) {
		return nil, nil, fmt.Errorf("%q is not a trip slug", slug)
	}
	dir := filepath.Join(dataDir, tripsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	tmp, err := os.CreateTemp(dir, ".trip-*.tmp")
	if err != nil {
		return nil, nil, err
	}
	name := tmp.Name()
	discard = func() { os.Remove(name) }
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		discard()
		return nil, nil, err
	}
	// fsync before the rename: the volume outlives the machine, and a
	// renamed-but-unflushed file would come back empty.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		discard()
		return nil, nil, err
	}
	if err := tmp.Close(); err != nil {
		discard()
		return nil, nil, err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		discard()
		return nil, nil, err
	}
	commit = func() error { return os.Rename(name, tripPath(dataDir, slug)) }
	return commit, discard, nil
}

// maxLineEndKm is how far a leg line's first or last point may sit from the
// checkpoint at that end of the leg. A routed line ends at its checkpoint, so
// 50 km is generous on purpose: it is not a judgement about the road, only
// the difference between "the line belongs to this leg" and "the line is
// somewhere else on the planet", which is what a line written [lat, lng]
// looks like. At longitudes inside ±90 such a line passes the range check
// below, and this is the only thing that stops it.
const maxLineEndKm = 50

// checkPlan is what one plan must satisfy before it is allowed near the
// database: legs numbered 1..n with a drawable line each, checkpoints
// numbered 1..m, and every line starting and ending near the checkpoints its
// leg starts and ends at. The pages index both by order.
func checkPlan(sd *seedData) error {
	lineEnds := map[int][2][]float64{} // per leg: the line's first and last point
	for i, l := range sd.Legs {
		if l.Ord != i+1 {
			return fmt.Errorf("legs must be numbered 1..%d without gaps; found leg %d in place %d", len(sd.Legs), l.Ord, i+1)
		}
		var g struct {
			Coordinates [][]float64 `json:"coordinates"`
		}
		if err := json.Unmarshal([]byte(l.Line), &g); err != nil || len(g.Coordinates) < 2 {
			return fmt.Errorf("leg %d has no line of at least two points", l.Ord)
		}
		for _, p := range g.Coordinates {
			if len(p) < 2 || math.Abs(p[0]) > 180 || math.Abs(p[1]) > 90 {
				return fmt.Errorf("leg %d has a point that is not [lng, lat]", l.Ord)
			}
		}
		if l.EstMiles < 0 {
			return fmt.Errorf("leg %d has negative miles", l.Ord)
		}
		lineEnds[l.Ord] = [2][]float64{g.Coordinates[0], g.Coordinates[len(g.Coordinates)-1]}
	}
	if len(sd.Checkpoints) == 0 {
		return fmt.Errorf("a trip needs at least one checkpoint")
	}
	for i, c := range sd.Checkpoints {
		if c.Ord != i+1 {
			return fmt.Errorf("checkpoints must be numbered 1..%d without gaps; found %d in place %d", len(sd.Checkpoints), c.Ord, i+1)
		}
		if math.Abs(c.Lng) > 180 || math.Abs(c.Lat) > 90 {
			return fmt.Errorf("checkpoint %d is not at a [lng, lat]", c.Ord)
		}
	}
	return checkLineEnds(sd, lineEnds)
}

// checkLineEnds holds every leg line to the checkpoints at its two ends. A
// leg ends at the last checkpoint filed on it, and starts where the leg
// before it ended; the first leg starts at its own first checkpoint. An end
// that has no checkpoint is not measured, but a leg with none at either end
// is refused: a line nothing can place is not a line this check passed.
//
// The refusal names the leg and the end. It never carries a coordinate: the
// message goes back to whoever pushed, and into a log.
func checkLineEnds(sd *seedData, lineEnds map[int][2][]float64) error {
	onLeg := map[int][]seedCP{}
	for _, c := range sd.Checkpoints { // already in order
		onLeg[c.LegOrd] = append(onLeg[c.LegOrd], c)
	}
	far := func(p []float64, c seedCP) bool {
		return haversineKm(p[1], p[0], c.Lat, c.Lng) > maxLineEndKm
	}
	for i, l := range sd.Legs {
		var start, end *seedCP
		if own := onLeg[l.Ord]; len(own) > 0 {
			end = &own[len(own)-1]
			if i == 0 && len(own) > 1 {
				start = &own[0]
			}
		}
		if i > 0 {
			if before := onLeg[sd.Legs[i-1].Ord]; len(before) > 0 {
				start = &before[len(before)-1]
			}
		}
		if start == nil && end == nil {
			return fmt.Errorf("leg %d has no checkpoint at either end, so its line cannot be placed", l.Ord)
		}
		first, last := lineEnds[l.Ord][0], lineEnds[l.Ord][1]
		if start != nil && far(first, *start) {
			return fmt.Errorf("leg %d: its line starts more than %d km from the checkpoint the leg starts at; "+
				"check that its points are [lng, lat]", l.Ord, maxLineEndKm)
		}
		if end != nil && far(last, *end) {
			return fmt.Errorf("leg %d: its line ends more than %d km from the checkpoint the leg ends at; "+
				"check that its points are [lng, lat]", l.Ord, maxLineEndKm)
		}
	}
	return nil
}

// checkTrip parses a pushed document completely — under its defaults and
// under every choice of every option — before a byte of it is written. It
// returns the document's slug.
func checkTrip(raw []byte) (string, error) {
	sd, err := parseSeedBytes(raw, "", nil)
	if err != nil {
		return "", err
	}
	if !validSlug(sd.Ride.Slug) {
		return "", fmt.Errorf("ride.slug %q must be lowercase letters, digits and single dashes", sd.Ride.Slug)
	}
	if sd.Ride.StartDate == "" {
		return "", fmt.Errorf("the trip has no start date: give ride.start_date, or a date on every leg")
	}
	if err := checkPlan(sd); err != nil {
		return "", err
	}
	for _, l := range sd.Legs {
		if l.Date == "" {
			return "", fmt.Errorf("leg %d has no date and none can be derived", l.Ord)
		}
	}
	for _, o := range sd.Options {
		for _, c := range o.Choices {
			alt, err := parseSeedBytes(raw, "", &Selection{Choices: map[string]string{o.ID: c.ID}})
			if err == nil {
				err = checkPlan(alt)
			}
			if err != nil {
				return "", fmt.Errorf("option %q choice %q: %w", o.ID, c.ID, err)
			}
		}
	}
	if _, err := publishPlan(raw); err != nil {
		return "", err
	}
	return sd.Ride.Slug, nil
}

// embeddedRideSlugs lists the rides the image itself carries. A pushed trip
// may not take one of their slugs: boot applies the embedded seed first and
// the two would overwrite each other on every deploy.
func embeddedRideSlugs() map[string]bool {
	out := map[string]bool{}
	names, _ := fs.Glob(seedFS, "seed/*.geojson")
	for _, name := range names {
		raw, err := seedFS.ReadFile(name)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
		if sd, err := parseSeedBytes(raw, base, nil); err == nil {
			out[sd.Ride.Slug] = true
		}
	}
	return out
}

// startNotApplied is the sentence a push carries when the document's start
// date lost to the saved selection's. The rule is deliberate; staying silent
// about it was the defect.
const startNotApplied = "the document's start date was not applied: this ride has a saved selection and its " +
	"start date stays in force. Move the start with POST /admin/plan (start=YYYY-MM-DD)"

// startOverridden reports whether the plan was dated from a saved selection
// instead of from the document that was just read.
func (sd *seedData) startOverridden() bool {
	return sd.DocStartDate != "" && sd.DocStartDate != sd.Selection.StartDate
}

// applyTrip puts a trip document into the database under its stored
// selection: created when the ride is new, refreshed under the guard when it
// is not.
func applyTrip(store *Store, raw []byte, slug string) (created bool, n RefreshCounts, sd *seedData, err error) {
	sd, err = parseSeedBytes(raw, slug, store.StoredSelection(slug))
	if err != nil {
		return false, n, nil, err
	}
	if sd.Ride.Slug != slug {
		return false, n, nil, fmt.Errorf("the document names ride %q, not %q", sd.Ride.Slug, slug)
	}
	if err := checkPlan(sd); err != nil {
		return false, n, nil, err
	}
	created, n, err = store.ApplyPlan(sd)
	return created, n, sd, err
}

// loadVolumeTrips is the boot half: every trip file on the volume goes
// through the same create-or-refresh path a push takes. A file that will not
// load is logged and skipped, never fatal — a bad trip file must not be able
// to keep the machine from booting, and the ride it would have refreshed is
// still in the database as it was.
func loadVolumeTrips(store *Store, dataDir string) (loaded, failed int) {
	names, _ := filepath.Glob(filepath.Join(dataDir, tripsDir, "*.geojson"))
	sort.Strings(names)
	embedded := embeddedRideSlugs()
	for _, name := range names {
		slug := strings.TrimSuffix(filepath.Base(name), ".geojson")
		fail := func(err error) {
			failed++
			log.Printf("WARN trip file %s not loaded: %v", filepath.Base(name), err)
		}
		if !validSlug(slug) {
			fail(errors.New("the file name is not a trip slug"))
			continue
		}
		if embedded[slug] {
			fail(errors.New("the slug belongs to a ride the image carries"))
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			fail(err)
			continue
		}
		created, n, sd, err := applyTrip(store, raw, slug)
		if err != nil {
			fail(err)
			continue
		}
		loaded++
		if created {
			log.Printf("trip %s created from the volume", slug)
		} else {
			log.Printf("trip %s refreshed from the volume: %s", slug, n)
		}
		if sd.startOverridden() {
			// That it happened, never the dates.
			log.Printf("trip %s: the file's start date was not applied, the saved selection's stays in force", slug)
		}
	}
	return loaded, failed
}

// --- the plan, as the planner page sees it -------------------------------------

// planLeg is one road the plan could take: a fixed leg, or one alternative of
// an option. Line has been through maskLine.
type planLeg struct {
	Ord    int             `json:"ord"`
	Day    int             `json:"day"`
	Slug   string          `json:"slug"`
	Name   string          `json:"name"`
	Hint   string          `json:"hint,omitempty"`
	Miles  float64         `json:"miles"`
	Hours  float64         `json:"hours"`
	Date   string          `json:"date,omitempty"` // only when the leg carries its own
	Option string          `json:"option,omitempty"`
	Choice string          `json:"choice,omitempty"`
	Color  string          `json:"color"`
	Line   json.RawMessage `json:"line"`
}

// planCP is one checkpoint the plan could have. Lat/Lng are the published
// point: a private place is its snapped cell.
type planCP struct {
	Ord       int     `json:"ord"`
	Leg       int     `json:"leg"`
	Name      string  `json:"name"`
	Label     string  `json:"label"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	TZ        string  `json:"tz"`
	Precision string  `json:"precision"`
	Terminal  bool    `json:"is_terminal"`
	Option    string  `json:"option,omitempty"`
	Choice    string  `json:"choice,omitempty"`
}

// planDoc is a whole trip with every alternative in it, already published.
// It is what the planner page embeds and what every total is computed from.
type planDoc struct {
	Ride         string       `json:"ride"`
	Name         string       `json:"name"`
	StartDate    string       `json:"start_date"`
	AvgMPH       float64      `json:"avg_mph"`
	LongDayHours float64      `json:"long_day_hours"`
	Options      []tripOption `json:"options"`
	Legs         []planLeg    `json:"legs"`
	Checkpoints  []planCP     `json:"checkpoints"`
}

// publishPlan turns a raw trip document into the one the planner may show.
// It is the store's fuzz seam applied to a file instead of to rows: every
// checkpoint goes through Checkpoint.fuzz and every line through maskLine,
// against the private places of ALL the alternatives at once — so a road the
// plan did not take is held to the same rule as the one it did.
func publishPlan(raw []byte) (*planDoc, error) {
	base, err := parseSeedBytes(raw, "", nil)
	if err != nil {
		return nil, err
	}
	doc := &planDoc{Ride: base.Ride.Slug, Name: base.Ride.Name, StartDate: base.Ride.StartDate,
		AvgMPH: base.AvgMPH, LongDayHours: base.LongDayHours, Options: base.Options,
		Legs: []planLeg{}, Checkpoints: []planCP{}}
	if doc.Options == nil {
		doc.Options = []tripOption{}
	}
	var legs []seedLeg
	var cps []seedCP
	legs = append(legs, base.Legs...)
	cps = append(cps, base.Checkpoints...)
	// The defaults carry every fixed feature and each option's default choice.
	// Each other choice contributes exactly the features tagged with it.
	for _, o := range base.Options {
		for _, c := range o.Choices {
			if c.ID == o.Default {
				continue
			}
			alt, err := parseSeedBytes(raw, "", &Selection{Choices: map[string]string{o.ID: c.ID}})
			if err != nil {
				return nil, fmt.Errorf("option %q choice %q: %w", o.ID, c.ID, err)
			}
			for _, l := range alt.Legs {
				if l.Option == o.ID && l.Choice == c.ID {
					legs = append(legs, l)
				}
			}
			for _, cp := range alt.Checkpoints {
				if cp.Option == o.ID && cp.Choice == c.ID {
					cps = append(cps, cp)
				}
			}
		}
	}
	var all []Checkpoint
	for _, c := range cps {
		pub := Checkpoint{Ord: c.Ord, Name: c.Name, Label: c.Label, Lat: c.Lat, Lng: c.Lng,
			TZ: c.TZ, Precision: c.Precision, IsTerminal: c.IsTerminal}
		pub.fuzz()
		all = append(all, pub)
		doc.Checkpoints = append(doc.Checkpoints, planCP{Ord: pub.Ord, Leg: c.LegOrd, Name: pub.Name,
			Label: pub.Label, Lat: pub.Lat, Lng: pub.Lng, TZ: pub.TZ, Precision: pub.Precision,
			Terminal: pub.IsTerminal, Option: c.Option, Choice: c.Choice})
	}
	fuzzed := fuzzedOf(all)
	for _, l := range legs {
		var g struct {
			Coordinates json.RawMessage `json:"coordinates"`
		}
		if err := json.Unmarshal([]byte(l.Line), &g); err != nil || len(g.Coordinates) == 0 {
			return nil, fmt.Errorf("leg %d has no line", l.Ord)
		}
		pl := planLeg{Ord: l.Ord, Day: l.Day, Slug: l.Slug, Name: l.Name, Hint: l.RouteHint,
			Miles: l.EstMiles, Hours: l.EstHours, Option: l.Option, Choice: l.Choice,
			Color: legColor(l.Ord), Line: maskLine(g.Coordinates, fuzzed)}
		if l.DateFixed {
			pl.Date = l.Date
		}
		doc.Legs = append(doc.Legs, pl)
	}
	return doc, nil
}

// planFromStore is the planner's view of a ride that has no trip file — an
// embedded seed. There are no alternatives to show, only the days; the legs
// and checkpoints come out of the store already fuzzed and masked.
func planFromStore(ride *Ride, legs []Leg) *planDoc {
	doc := &planDoc{Ride: ride.Slug, Name: ride.Name, StartDate: ride.StartDate,
		Options: []tripOption{}, Legs: []planLeg{}, Checkpoints: []planCP{}}
	for i, l := range legs {
		doc.Legs = append(doc.Legs, planLeg{Ord: l.Ord, Day: i + 1, Slug: l.Slug, Name: l.Name,
			Hint: l.RouteHint, Miles: l.EstMiles, Hours: l.EstHours, Date: l.Date,
			Color: legColor(l.Ord), Line: l.Line})
		for _, c := range l.Checkpoints {
			doc.Checkpoints = append(doc.Checkpoints, planCP{Ord: c.Ord, Leg: l.Ord, Name: c.Name,
				Label: c.Label, Lat: c.Lat, Lng: c.Lng, TZ: c.TZ, Precision: c.Precision,
				Terminal: c.IsTerminal})
		}
	}
	return doc
}

// choices resolves a selection against the document: the document's default
// for every option, overridden by each layer in turn where that layer names
// a choice the option really has.
func (d *planDoc) choices(layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, o := range d.Options {
		out[o.ID] = o.Default
		for _, layer := range layers {
			for _, c := range o.Choices {
				if layer[o.ID] == c.ID {
					out[o.ID] = c.ID
				}
			}
		}
	}
	return out
}

// hasChoice reports whether an option of that id offers that choice.
func (d *planDoc) hasChoice(option, choice string) bool {
	for _, o := range d.Options {
		if o.ID != option {
			continue
		}
		for _, c := range o.Choices {
			if c.ID == choice {
				return true
			}
		}
	}
	return false
}

func (d *planDoc) in(option, choice string, choices map[string]string) bool {
	return option == "" || choices[option] == choice
}

type planDay struct {
	Day   int     `json:"day"`
	Date  string  `json:"date"`
	Name  string  `json:"name"`
	Legs  []int   `json:"legs"`
	Miles float64 `json:"miles"`
	Hours float64 `json:"hours"`
	// Long is judged on the unrounded hours, so a day is not flagged or
	// spared by the rounding of its own display.
	Long  bool   `json:"long_day"`
	Color string `json:"color"`
}

// planSummary is one selection worked out: the days, the totals, the
// checkpoints. The page renders it, /plan.json serves it, and the script in
// the page computes the same numbers the same way when a visitor switches.
type planSummary struct {
	Ride            string            `json:"ride"`
	Name            string            `json:"name"`
	StartDate       string            `json:"start_date"`
	EndDate         string            `json:"end_date"`
	Selection       map[string]string `json:"selection"`
	AvgMPH          float64           `json:"avg_mph"`
	LongDayHours    float64           `json:"long_day_hours"`
	Days            []planDay         `json:"days"`
	DayCount        int               `json:"day_count"`
	LongDays        int               `json:"long_days"`
	TotalMiles      float64           `json:"total_miles"`
	TotalMilesShown string            `json:"total_miles_shown"`
	TotalHours      float64           `json:"total_hours"`
	Checkpoints     []planCP          `json:"checkpoints"`
}

func (d *planDoc) summarize(choices map[string]string, start string) planSummary {
	s := planSummary{Ride: d.Ride, Name: d.Name, StartDate: start, Selection: choices,
		AvgMPH: d.AvgMPH, LongDayHours: d.LongDayHours, Days: []planDay{}, Checkpoints: []planCP{}}
	var startDay time.Time
	if t, err := time.Parse(dateLayout, start); err == nil {
		startDay = t
	}
	byDay := map[int]*planDay{}
	rawMiles := map[int]float64{}
	rawHours := map[int]float64{}
	var order []int
	total := 0.0
	legs := append([]planLeg(nil), d.Legs...)
	sort.SliceStable(legs, func(i, j int) bool { return legs[i].Ord < legs[j].Ord })
	for _, l := range legs {
		if !d.in(l.Option, l.Choice, choices) {
			continue
		}
		pd := byDay[l.Day]
		if pd == nil {
			pd = &planDay{Day: l.Day, Date: l.Date, Color: l.Color}
			if l.Date == "" && !startDay.IsZero() {
				pd.Date = startDay.AddDate(0, 0, l.Day-1).Format(dateLayout)
			}
			byDay[l.Day] = pd
			order = append(order, l.Day)
		}
		pd.Legs = append(pd.Legs, l.Ord)
		if pd.Name != "" {
			pd.Name += ", then "
		}
		pd.Name += l.Name
		rawMiles[l.Day] += l.Miles
		if d.AvgMPH > 0 {
			rawHours[l.Day] += l.Miles / d.AvgMPH
		} else {
			rawHours[l.Day] += l.Hours
		}
		total += l.Miles
	}
	sort.Ints(order)
	hours := 0.0
	for _, day := range order {
		pd := byDay[day]
		pd.Miles = round1(rawMiles[day])
		pd.Hours = round1(rawHours[day])
		pd.Long = d.LongDayHours > 0 && rawHours[day] > d.LongDayHours
		if pd.Long {
			s.LongDays++
		}
		hours += rawHours[day]
		if pd.Date > s.EndDate {
			s.EndDate = pd.Date
		}
		s.Days = append(s.Days, *pd)
	}
	s.DayCount = len(s.Days)
	s.TotalMiles = round1(total)
	s.TotalMilesShown = thousands(int(math.Round(s.TotalMiles)))
	s.TotalHours = round1(hours)
	for _, c := range d.Checkpoints {
		if d.in(c.Option, c.Choice, choices) {
			s.Checkpoints = append(s.Checkpoints, c)
		}
	}
	sort.SliceStable(s.Checkpoints, func(i, j int) bool { return s.Checkpoints[i].Ord < s.Checkpoints[j].Ord })
	return s
}

// thousands writes 1986 as "1,986".
func thousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}
