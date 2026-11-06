package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The converter: `roadtrip -convert route.json -slug … -start … -zones …`.
//
// A planning tool hands over a route document in its own shape (schema
// roadtrip-route/0.1): stops, option groups, and routed segments with the
// geometry as [lat, lng]. This turns it into the app's trip file — the seed
// GeoJSON shape plus options — instead of teaching the app a second schema.
//
// What it deliberately does not carry: the planning tool's free-text notes and
// tags on a stop. A checkpoint's label is derived from what the stop IS (the
// start, a night, the finish), because a label is rendered to every visitor
// and a planning note was written for one reader.
//
// What it refuses: a stop that becomes a checkpoint and has no IANA zone.
// Every time on the site is UTC plus the zone it happened in, and there is no
// ride-wide zone to fall back on, so a zone is not something to guess.
//
// The output is a trip file: it belongs in the local data dir (gitignored)
// and on the volume by push. It never belongs in the repository.

const routeSchemaPrefix = "roadtrip-route/"

type routeStop struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
	Kind string  `json:"kind"`
	TZ   string  `json:"tz"`
}

type routeSegment struct {
	ID       string      `json:"id"`
	Day      int         `json:"day"`
	Label    string      `json:"label"`
	From     string      `json:"from"`
	To       string      `json:"to"`
	Via      []string    `json:"via"`
	Miles    float64     `json:"miles"`
	Geometry [][]float64 `json:"geometry"`
}

type routeOption struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Default string `json:"default"`
	Choices []struct {
		ID       string   `json:"id"`
		Label    string   `json:"label"`
		Detail   string   `json:"detail"`
		Segments []string `json:"segments"`
	} `json:"choices"`
}

type routeDoc struct {
	Schema       string         `json:"schema"`
	Name         string         `json:"name"`
	AvgMPH       float64        `json:"avg_mph"`
	LongDayHours float64        `json:"long_day_hours"`
	Stops        []routeStop    `json:"stops"`
	Options      []routeOption  `json:"options"`
	Segments     []routeSegment `json:"segments"`
}

// convertOptions is what the route document does not know about itself.
type convertOptions struct {
	Slug  string
	Start string // YYYY-MM-DD
	Name  string // optional; the document's own name otherwise
	Zones string // stop=Zone,stop=Zone — for stops that carry no tz
}

type tripFeature struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
	Geometry   map[string]any `json:"geometry"`
}

type tripFile struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Ride struct {
		Slug      string `json:"slug"`
		Name      string `json:"name"`
		StartDate string `json:"start_date"`
		Mode      string `json:"mode"`
	} `json:"ride"`
	AvgMPH       float64       `json:"avg_mph,omitempty"`
	LongDayHours float64       `json:"long_day_hours,omitempty"`
	Options      []tripOption  `json:"options"`
	Features     []tripFeature `json:"features"`
}

// placement is where a stop sits in one plan: its checkpoint order, the leg
// it belongs to, and whether a segment ends there (a night) or passes it.
type placement struct {
	Present  bool
	Ord, Leg int
	Terminal bool
}

func parseZones(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("zones: %q is not stop=Zone", part)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

func slugify(s string) string {
	var b strings.Builder
	dash := true // no leading dash
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// convertRoute turns a route document into a trip file.
func convertRoute(raw []byte, opt convertOptions) ([]byte, error) {
	var rd routeDoc
	if err := json.Unmarshal(raw, &rd); err != nil {
		return nil, fmt.Errorf("not JSON: %w", err)
	}
	if !strings.HasPrefix(rd.Schema, routeSchemaPrefix) {
		return nil, fmt.Errorf("schema %q is not %s*", rd.Schema, routeSchemaPrefix)
	}
	if !validSlug(opt.Slug) {
		return nil, fmt.Errorf("slug %q must be lowercase letters, digits and single dashes", opt.Slug)
	}
	if _, err := time.Parse(dateLayout, opt.Start); err != nil {
		return nil, fmt.Errorf("start %q is not YYYY-MM-DD", opt.Start)
	}
	zones, err := parseZones(opt.Zones)
	if err != nil {
		return nil, err
	}
	stops := map[string]routeStop{}
	for _, s := range rd.Stops {
		if s.ID == "" {
			return nil, fmt.Errorf("a stop has no id")
		}
		stops[s.ID] = s
	}
	for id := range zones {
		if _, ok := stops[id]; !ok {
			return nil, fmt.Errorf("zones names stop %q, which the document does not have", id)
		}
	}
	segs := map[string]routeSegment{}
	for _, s := range rd.Segments {
		if s.ID == "" || s.Day < 1 {
			return nil, fmt.Errorf("segment %q needs an id and a day of 1 or more", s.ID)
		}
		if len(s.Geometry) < 2 {
			return nil, fmt.Errorf("segment %q has no geometry", s.ID)
		}
		segs[s.ID] = s
	}

	// Which choice owns each segment. A segment no choice names is fixed.
	type owner struct{ Option, Choice string }
	owners := map[string]owner{}
	var options []tripOption
	for _, o := range rd.Options {
		to := tripOption{ID: o.ID, Title: o.Title, Default: o.Default}
		for _, c := range o.Choices {
			to.Choices = append(to.Choices, tripChoice{ID: c.ID, Label: c.Label, Detail: c.Detail})
			for _, id := range c.Segments {
				if _, ok := segs[id]; !ok {
					return nil, fmt.Errorf("option %q choice %q names segment %q, which does not exist", o.ID, c.ID, id)
				}
				if prev, taken := owners[id]; taken {
					return nil, fmt.Errorf("segment %q belongs to both %s/%s and %s/%s", id, prev.Option, prev.Choice, o.ID, c.ID)
				}
				owners[id] = owner{o.ID, c.ID}
			}
		}
		options = append(options, to)
	}
	if _, err := checkOptions(options); err != nil {
		return nil, err
	}

	// Every full combination of choices is one plan. Walk each, and note where
	// every stop lands in it.
	combos := []map[string]string{{}}
	for _, o := range options {
		var next []map[string]string
		for _, base := range combos {
			for _, c := range o.Choices {
				m := map[string]string{}
				for k, v := range base {
					m[k] = v
				}
				m[o.ID] = c.ID
				next = append(next, m)
			}
		}
		combos = next
		if len(combos) > 1024 {
			return nil, fmt.Errorf("too many combinations of choices to check")
		}
	}
	placed := make([]map[string]placement, len(combos))
	for i, combo := range combos {
		var day []routeSegment
		for _, s := range rd.Segments {
			if o, owned := owners[s.ID]; !owned || combo[o.Option] == o.Choice {
				day = append(day, s)
			}
		}
		sort.SliceStable(day, func(a, b int) bool { return day[a].Day < day[b].Day })
		placed[i] = map[string]placement{}
		ord := 0
		put := func(id string, leg int, terminal bool, seg string) error {
			st, ok := stops[id]
			if !ok {
				if terminal {
					return fmt.Errorf("segment %q runs to %q, which is not a stop", seg, id)
				}
				return nil // a via that is only a place name on the planning map
			}
			if _, twice := placed[i][st.ID]; twice {
				return fmt.Errorf("stop %q is visited twice in one plan", id)
			}
			ord++
			placed[i][st.ID] = placement{Present: true, Ord: ord, Leg: leg, Terminal: terminal}
			return nil
		}
		for n, s := range day {
			if s.Day != n+1 {
				return nil, fmt.Errorf("a plan needs exactly one segment for each day 1..%d; segment %q is day %d in place %d",
					len(day), s.ID, s.Day, n+1)
			}
			if n == 0 {
				if err := put(s.From, s.Day, true, s.ID); err != nil {
					return nil, err
				}
			} else if day[n-1].To != s.From {
				return nil, fmt.Errorf("segment %q starts at %q but the day before ends at %q", s.ID, s.From, day[n-1].To)
			}
			for _, v := range s.Via {
				if err := put(v, s.Day, false, s.ID); err != nil {
					return nil, err
				}
			}
			if err := put(s.To, s.Day, true, s.ID); err != nil {
				return nil, err
			}
		}
	}

	tf := tripFile{Type: "FeatureCollection", Name: rd.Name, AvgMPH: rd.AvgMPH,
		LongDayHours: rd.LongDayHours, Options: options, Features: []tripFeature{}}
	if tf.Options == nil {
		tf.Options = []tripOption{}
	}
	if opt.Name != "" {
		tf.Name = opt.Name
	}
	tf.Ride.Slug, tf.Ride.Name, tf.Ride.StartDate, tf.Ride.Mode = opt.Slug, tf.Name, opt.Start, "CAR"

	// Legs: one LineString per segment, geometry flipped to [lng, lat].
	usedSlug := map[string]bool{}
	for _, s := range rd.Segments {
		from, ok1 := stops[s.From]
		to, ok2 := stops[s.To]
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("segment %q runs between stops the document does not have", s.ID)
		}
		slug := slugify(s.ID)
		if slug == "" || usedSlug[slug] {
			return nil, fmt.Errorf("segment id %q does not make a unique slug", s.ID)
		}
		usedSlug[slug] = true
		line := make([][]float64, 0, len(s.Geometry))
		for _, p := range s.Geometry {
			if len(p) < 2 {
				return nil, fmt.Errorf("segment %q has a point that is not [lat, lng]", s.ID)
			}
			line = append(line, []float64{p[1], p[0]})
		}
		props := map[string]any{"leg": s.Day, "day": s.Day, "slug": slug, "kind": "PLANNED", "mode": "CAR",
			"name": from.Name + " → " + to.Name, "est_miles": s.Miles, "route_hint": s.Label}
		if o, owned := owners[s.ID]; owned {
			props["option"], props["choice"] = o.Option, o.Choice
		}
		tf.Features = append(tf.Features, tripFeature{Type: "Feature", Properties: props,
			Geometry: map[string]any{"type": "LineString", "coordinates": line}})
	}

	// Checkpoints: a stop that sits in the same place in every plan is written
	// once. One whose place depends on a single option is written once per
	// choice of that option. Anything else the trip shape cannot say.
	checkpoint := func(st routeStop, p placement, o, c string) (tripFeature, error) {
		tz := st.TZ
		if tz == "" {
			tz = zones[st.ID]
		}
		if tz == "" {
			return tripFeature{}, fmt.Errorf("stop %q has no time zone: add \"tz\" to the stop or name it in -zones (%s=Area/City)", st.ID, st.ID)
		}
		if _, err := time.LoadLocation(tz); err != nil {
			return tripFeature{}, fmt.Errorf("stop %q: %q is not an IANA zone", st.ID, tz)
		}
		label, precision := "Waypoint", precisionApprox
		switch st.Kind {
		case "start":
			label, precision = "Start", precisionFuzzed
		case "end":
			label, precision = "Finish", precisionFuzzed
		case "border":
			label = "Border crossing"
		default:
			if p.Terminal {
				label = fmt.Sprintf("Night %d", p.Leg)
			}
		}
		props := map[string]any{"kind": "checkpoint", "order": p.Ord, "leg": p.Leg, "name": st.Name,
			"label": label, "tz": tz, "precision": precision, "is_terminal": p.Terminal}
		if o != "" {
			props["option"], props["choice"] = o, c
		}
		return tripFeature{Type: "Feature", Properties: props,
			Geometry: map[string]any{"type": "Point", "coordinates": []float64{st.Lng, st.Lat}}}, nil
	}
	for _, st := range rd.Stops {
		same, anywhere := true, false
		for i := range combos {
			if placed[i][st.ID].Present {
				anywhere = true
			}
			if placed[i][st.ID] != placed[0][st.ID] {
				same = false
			}
		}
		if !anywhere {
			continue // a stop no plan visits
		}
		if same {
			f, err := checkpoint(st, placed[0][st.ID], "", "")
			if err != nil {
				return nil, err
			}
			tf.Features = append(tf.Features, f)
			continue
		}
		decided := false
		for _, o := range options {
			byChoice := map[string]placement{}
			ok := true
			for i, combo := range combos {
				p, seen := byChoice[combo[o.ID]]
				if !seen {
					byChoice[combo[o.ID]] = placed[i][st.ID]
				} else if p != placed[i][st.ID] {
					ok = false
				}
			}
			if !ok {
				continue
			}
			for _, c := range o.Choices {
				if p := byChoice[c.ID]; p.Present {
					f, err := checkpoint(st, p, o.ID, c.ID)
					if err != nil {
						return nil, err
					}
					tf.Features = append(tf.Features, f)
				}
			}
			decided = true
			break
		}
		if !decided {
			return nil, fmt.Errorf("stop %q changes place under more than one option; a trip file ties a checkpoint to one option at most", st.ID)
		}
	}

	out, err := json.MarshalIndent(tf, "", "  ")
	if err != nil {
		return nil, err
	}
	out = append(out, '\n')
	// The converter's output has to be a document the app itself accepts.
	if slug, err := checkTrip(out); err != nil {
		return nil, fmt.Errorf("the converted trip does not load: %w", err)
	} else if slug != opt.Slug {
		return nil, fmt.Errorf("the converted trip names ride %q, not %q", slug, opt.Slug)
	}
	return out, nil
}

// runConvert is the command: read, convert, write, and say what was written.
// With no -out the file goes where the app reads trips from.
func runConvert(in, out, dataDir string, opt convertOptions) (string, error) {
	raw, err := os.ReadFile(in)
	if err != nil {
		return "", err
	}
	trip, err := convertRoute(raw, opt)
	if err != nil {
		return "", err
	}
	if out == "" {
		commit, discard, err := stageTrip(dataDir, opt.Slug, trip)
		if err != nil {
			return "", err
		}
		if err := commit(); err != nil {
			discard()
			return "", err
		}
		out = tripPath(dataDir, opt.Slug)
	} else {
		if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(out, trip, 0o600); err != nil {
			return "", err
		}
	}
	doc, err := publishPlan(trip)
	if err != nil {
		return "", err
	}
	sum := doc.summarize(doc.choices(), doc.StartDate)
	return fmt.Sprintf("wrote %s: %d option group(s), default plan %s mi over %d day(s), %d long day(s)",
		out, len(doc.Options), fmtMiles(sum.TotalMiles), sum.DayCount, sum.LongDays), nil
}
