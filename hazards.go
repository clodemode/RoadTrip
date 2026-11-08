package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Hazards on the road ahead: weather alerts from the US National Weather
// Service and traffic incidents from TomTom. Both are asked by the server,
// never by the phone, so the key stays a Fly secret and one answer serves
// every refresh inside the cache window.
//
// The rule that matters is the honest one. A source that has no key, errored
// or timed out is shown as "unknown" with its reason; it is never an empty
// list and never "clear". "Clear" means the source answered with nothing.

const (
	hazardItems   = "items"
	hazardClear   = "clear"
	hazardUnknown = "unknown"

	hazardCacheTTL   = 10 * time.Minute // an answer
	hazardUnknownTTL = time.Minute      // a failure: retry soon, but not on every refresh
	hazardTimeout    = 4 * time.Second  // per source, so a page can never hang on a third party
	hazardMaxChips   = 4

	// NWS asks for a descriptive User-Agent with a way to reach the operator.
	// This is the project's public page, not a person.
	nwsUserAgent = "RoadTrip/1.0 (roadtrip.opeongo.net; https://github.com/clodemode/RoadTrip)"
)

type ll struct{ Lat, Lng float64 }

// hazardChip is one line on the screen. State is items, clear or unknown.
type hazardChip struct {
	Source string `json:"source"`
	State  string `json:"state"`
	Text   string `json:"text"`
}

type hazardSvc struct {
	nwsBase string
	gcBase  string // Environment Canada GeoMet, asked only where the US source says a point is out of bounds
	ttBase  string
	key     string
	client  *http.Client
	timeout time.Duration
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]hazardEntry
}

type hazardEntry struct {
	at    time.Time
	ttl   time.Duration
	chips []hazardChip
}

func newHazardSvc(tomtomKey string) *hazardSvc {
	return &hazardSvc{
		nwsBase: "https://api.weather.gov",
		gcBase:  "https://api.weather.gc.ca",
		ttBase:  "https://api.tomtom.com",
		key:     tomtomKey,
		client:  &http.Client{},
		timeout: hazardTimeout,
		now:     time.Now,
		cache:   map[string]hazardEntry{},
	}
}

// ForLeg returns the weather and traffic chips for one leg's sample points,
// cached for ten minutes (a failure for one).
func (s *hazardSvc) ForLeg(ctx context.Context, key string, pts []ll) []hazardChip {
	s.mu.Lock()
	if e, ok := s.cache[key]; ok && s.now().Sub(e.at) < e.ttl {
		out := append([]hazardChip(nil), e.chips...)
		s.mu.Unlock()
		return out
	}
	s.mu.Unlock()

	var w, t []hazardChip
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); w = s.Weather(ctx, pts) }()
	go func() { defer wg.Done(); t = s.Traffic(ctx, pts) }()
	wg.Wait()
	chips := append(w, t...)

	ttl := hazardCacheTTL
	for _, c := range chips {
		if c.State == hazardUnknown {
			ttl = hazardUnknownTTL
		}
	}
	s.mu.Lock()
	s.cache[key] = hazardEntry{at: s.now(), ttl: ttl, chips: chips}
	s.mu.Unlock()
	return append([]hazardChip(nil), chips...)
}

// reason names why a request failed, in one word, without ever carrying the
// request's URL (which holds the TomTom key) into a chip or a log.
func reason(err error) string {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return "timeout"
	}
	return "error"
}

// errOutOfCoverage is the one 400 that means something other than "failed":
// the US National Weather Service says the point is outside its coverage. It
// carries no URL and no body.
var errOutOfCoverage = errors.New("out of coverage")

// outOfBounds reads a problem-details body and reports whether its detail
// says the point is out of bounds. Any other 400, or a body that does not
// parse, is an ordinary failure.
func outOfBounds(body io.Reader) bool {
	var pd struct {
		Detail string `json:"detail"`
	}
	if json.NewDecoder(io.LimitReader(body, 16<<10)).Decode(&pd) != nil {
		return false
	}
	return strings.Contains(strings.ToLower(pd.Detail), "out of bounds")
}

// getJSON fetches one URL under the per-source timeout and decodes it. Every
// error it returns is already scrubbed: no URL, no key.
func (s *hazardSvc) getJSON(ctx context.Context, u string, hdr map[string]string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return errors.New("bad request")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return errors.New(reason(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusBadRequest && outOfBounds(resp.Body) {
			return errOutOfCoverage
		}
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	if err := dec.Decode(out); err != nil {
		// A body cut off by the timeout reads as a timeout.
		if ctx.Err() != nil {
			return errors.New("timeout")
		}
		return errors.New("error")
	}
	return nil
}

// fanOut runs one request per point and folds the answers. A point that failed
// and no item anywhere means the source cannot say "clear".
func fanOut[T any](n int, fetch func(i int) ([]T, error)) (items []T, failed int, firstErr error) {
	type res struct {
		items []T
		err   error
	}
	rs := make([]res, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); rs[i].items, rs[i].err = fetch(i) }(i)
	}
	wg.Wait()
	for _, r := range rs {
		if r.err != nil {
			failed++
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		items = append(items, r.items...)
	}
	return items, failed, firstErr
}

func unknownChip(source, why string) []hazardChip {
	return []hazardChip{{Source: source, State: hazardUnknown, Text: source + ": unknown (" + why + ")"}}
}

func shortWhy(err error) string {
	if err != nil && err.Error() == "timeout" {
		return "timeout"
	}
	return "error"
}

// --- weather ---------------------------------------------------------------

type nwsAlert struct{ id, event, severity, area string }

// gcPad is how far, in degrees, the box around a Canadian point reaches on
// each side. The alerts are polygons, so a small box finds the ones it is in.
const gcPad = 0.02

func (s *hazardSvc) Weather(ctx context.Context, pts []ll) []hazardChip {
	if len(pts) == 0 {
		return unknownChip("Weather", "no route")
	}
	alerts, failed, err := fanOut(len(pts), func(i int) ([]nwsAlert, error) {
		var body struct {
			Features []struct {
				Properties struct {
					ID       string `json:"id"`
					Event    string `json:"event"`
					Severity string `json:"severity"`
					AreaDesc string `json:"areaDesc"`
				} `json:"properties"`
			} `json:"features"`
		}
		q := url.Values{"point": {strconv.FormatFloat(pts[i].Lat, 'f', 4, 64) + "," + strconv.FormatFloat(pts[i].Lng, 'f', 4, 64)}}
		if e := s.getJSON(ctx, s.nwsBase+"/alerts/active?"+q.Encode(), map[string]string{
			"User-Agent": nwsUserAgent, "Accept": "application/geo+json",
		}, &body); e != nil {
			if errors.Is(e, errOutOfCoverage) {
				return s.canadaAlerts(ctx, pts[i])
			}
			return nil, e
		}
		var out []nwsAlert
		for _, f := range body.Features {
			p := f.Properties
			out = append(out, nwsAlert{p.ID, p.Event, p.Severity, p.AreaDesc})
		}
		return out, nil
	})
	if len(alerts) == 0 {
		if failed > 0 {
			return unknownChip("Weather", shortWhy(err))
		}
		return []hazardChip{{Source: "Weather", State: hazardClear, Text: "Weather: no active alerts"}}
	}
	rank := map[string]int{"Extreme": 0, "Severe": 1, "Moderate": 2, "Minor": 3}
	seen := map[string]bool{}
	var uniq []nwsAlert
	for _, a := range alerts {
		k := a.id
		if k == "" {
			k = a.event + "|" + a.area
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		uniq = append(uniq, a)
	}
	sort.SliceStable(uniq, func(i, j int) bool {
		ri, ok := rank[uniq[i].severity]
		if !ok {
			ri = 4
		}
		rj, ok := rank[uniq[j].severity]
		if !ok {
			rj = 4
		}
		return ri < rj
	})
	var chips []hazardChip
	for _, a := range uniq {
		txt := "Weather: " + a.event
		if a.area != "" {
			txt += ", " + firstArea(a.area)
		}
		chips = append(chips, hazardChip{Source: "Weather", State: hazardItems, Text: txt})
	}
	return capChips("Weather", chips, failed > 0)
}

// canadaAlerts asks Environment Canada's GeoMet API for the alerts in a small
// box around one point, for a point the US source called out of bounds. Only
// the English text is read. An alert that has ended, was cancelled or has
// expired is not active and gives no chip. Canada's levels map onto the NWS
// ranks the chips are sorted by: a red warning is Extreme, a warning Severe, a
// watch Moderate, an advisory or a statement Minor.
func (s *hazardSvc) canadaAlerts(ctx context.Context, p ll) ([]nwsAlert, error) {
	var body struct {
		Features []struct {
			ID         string `json:"id"`
			Properties struct {
				Type       string `json:"alert_type"`
				Name       string `json:"alert_name_en"`
				Area       string `json:"feature_name_en"`
				Status     string `json:"status_en"`
				Expiration string `json:"expiration_datetime"`
				Colour     string `json:"risk_colour_en"`
			} `json:"properties"`
		} `json:"features"`
	}
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
	q := url.Values{
		"f":     {"json"},
		"limit": {"50"},
		"bbox":  {f(p.Lng-gcPad) + "," + f(p.Lat-gcPad) + "," + f(p.Lng+gcPad) + "," + f(p.Lat+gcPad)},
	}
	if e := s.getJSON(ctx, s.gcBase+"/collections/weather-alerts/items?"+q.Encode(), map[string]string{
		"User-Agent": nwsUserAgent, "Accept": "application/geo+json",
	}, &body); e != nil {
		return nil, e
	}
	var out []nwsAlert
	for _, ft := range body.Features {
		pr := ft.Properties
		switch strings.ToLower(pr.Status) {
		case "ended", "cancelled", "canceled":
			continue
		}
		if t, e := time.Parse(time.RFC3339, pr.Expiration); e == nil && !t.After(s.now()) {
			continue
		}
		name := strings.TrimSpace(pr.Name)
		if name == "" {
			name = strings.TrimSpace(pr.Type)
		}
		if name == "" {
			continue
		}
		sev := "Minor"
		switch strings.ToLower(pr.Type) {
		case "warning":
			sev = "Severe"
			if strings.EqualFold(pr.Colour, "red") {
				sev = "Extreme"
			}
		case "watch":
			sev = "Moderate"
		}
		out = append(out, nwsAlert{ft.ID, upperFirst(name), sev, strings.TrimSpace(pr.Area)})
	}
	return out, nil
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func firstArea(a string) string {
	if i := strings.IndexAny(a, ";"); i > 0 {
		return strings.TrimSpace(a[:i])
	}
	return strings.TrimSpace(a)
}

// capChips keeps the screen short: the worst few, then a count. partial adds
// a note when some points could not be asked, so a short list is not read as
// the whole picture.
func capChips(source string, chips []hazardChip, partial bool) []hazardChip {
	if len(chips) > hazardMaxChips {
		more := len(chips) - hazardMaxChips
		chips = append(chips[:hazardMaxChips:hazardMaxChips], hazardChip{Source: source, State: hazardItems, Text: fmt.Sprintf("%s: and %d more", source, more)})
	}
	if partial {
		chips = append(chips, hazardChip{Source: source, State: hazardUnknown, Text: source + ": some of the route could not be checked"})
	}
	return chips
}

// --- traffic ---------------------------------------------------------------

type ttIncident struct {
	id    string
	mag   int
	cat   int
	text  string
	delay int
}

func (s *hazardSvc) Traffic(ctx context.Context, pts []ll) []hazardChip {
	if s.key == "" {
		return unknownChip("Traffic", "no key")
	}
	if len(pts) == 0 {
		return unknownChip("Traffic", "no route")
	}
	incs, failed, err := fanOut(len(pts), func(i int) ([]ttIncident, error) {
		const dLat, dLng = 0.3, 0.35 // a box well under TomTom's area limit
		p := pts[i]
		bbox := fmt.Sprintf("%.4f,%.4f,%.4f,%.4f", p.Lng-dLng, p.Lat-dLat, p.Lng+dLng, p.Lat+dLat)
		q := url.Values{
			"key":                {s.key},
			"bbox":               {bbox},
			"fields":             {"{incidents{type,properties{id,iconCategory,magnitudeOfDelay,events{description},from,to,roadNumbers,delay}}}"},
			"language":           {"en-GB"},
			"timeValidityFilter": {"present"},
		}
		var body struct {
			Incidents []struct {
				Properties struct {
					ID     string `json:"id"`
					Icon   int    `json:"iconCategory"`
					Mag    int    `json:"magnitudeOfDelay"`
					Events []struct {
						Description string `json:"description"`
					} `json:"events"`
					From  string   `json:"from"`
					To    string   `json:"to"`
					Roads []string `json:"roadNumbers"`
					Delay int      `json:"delay"`
				} `json:"properties"`
			} `json:"incidents"`
		}
		if e := s.getJSON(ctx, s.ttBase+"/traffic/services/5/incidentDetails?"+q.Encode(), nil, &body); e != nil {
			return nil, e
		}
		var out []ttIncident
		for _, in := range body.Incidents {
			p := in.Properties
			desc := ""
			if len(p.Events) > 0 {
				desc = p.Events[0].Description
			}
			var b strings.Builder
			if len(p.Roads) > 0 {
				b.WriteString(strings.Join(p.Roads, "/") + " ")
			}
			b.WriteString(desc)
			if p.From != "" && p.To != "" {
				b.WriteString(", " + p.From + " to " + p.To)
			}
			if p.Delay >= 60 {
				b.WriteString(fmt.Sprintf(" (+%d min)", int(math.Round(float64(p.Delay)/60))))
			}
			out = append(out, ttIncident{id: p.ID, mag: p.Mag, cat: p.Icon, text: strings.TrimSpace(b.String()), delay: p.Delay})
		}
		return out, nil
	})
	if len(incs) == 0 {
		if failed > 0 {
			return unknownChip("Traffic", shortWhy(err))
		}
		return []hazardChip{{Source: "Traffic", State: hazardClear, Text: "Traffic: no incidents"}}
	}
	seen := map[string]bool{}
	var major []ttIncident
	minor := 0
	for _, in := range incs {
		if in.id != "" {
			if seen[in.id] {
				continue
			}
			seen[in.id] = true
		}
		if in.mag >= 2 || in.cat == 8 {
			major = append(major, in)
		} else {
			minor++
		}
	}
	if len(major) == 0 {
		return []hazardChip{{Source: "Traffic", State: hazardClear, Text: fmt.Sprintf("Traffic: no major incidents (%d minor)", minor)}}
	}
	// A closure outranks any delay; then the bigger delay.
	weight := func(in ttIncident) int {
		if in.cat == 8 || in.mag == 4 {
			return 100
		}
		return in.mag
	}
	sort.SliceStable(major, func(i, j int) bool { return weight(major[i]) > weight(major[j]) })
	var chips []hazardChip
	for _, in := range major {
		chips = append(chips, hazardChip{Source: "Traffic", State: hazardItems, Text: "Traffic: " + in.text})
	}
	return capChips("Traffic", chips, failed > 0)
}

// --- where to ask ----------------------------------------------------------

// legSamples picks up to n points along a leg's line, evenly by vertex, and
// drops any that sit within lineMaskKm of a private place: the question goes
// to third parties, and a point near a fuzzed checkpoint is not ours to send.
func legSamples(line json.RawMessage, n int, fuzzed []Checkpoint) []ll {
	var pts [][]float64
	if json.Unmarshal(line, &pts) != nil || len(pts) == 0 || n <= 0 {
		return nil
	}
	var out []ll
	for i := 0; i < n; i++ {
		idx := 0
		if n > 1 {
			idx = i * (len(pts) - 1) / (n - 1)
		}
		p := pts[idx]
		if len(p) < 2 {
			continue
		}
		if !nearFuzzed(p[1], p[0], fuzzed) {
			out = append(out, ll{p[1], p[0]})
		}
	}
	return out
}

// nearFuzzed reports whether a point is within lineMaskKm of a private place.
func nearFuzzed(lat, lng float64, fuzzed []Checkpoint) bool {
	for _, f := range fuzzed {
		if haversineKm(lat, lng, f.Lat, f.Lng) < lineMaskKm {
			return true
		}
	}
	return false
}

// dropNearFuzzed removes the points a hazard lookup must not be asked about:
// the same rule legSamples applies to a planned leg, for a routed line.
func dropNearFuzzed(pts []ll, fuzzed []Checkpoint) []ll {
	var out []ll
	for _, p := range pts {
		if !nearFuzzed(p.Lat, p.Lng, fuzzed) {
			out = append(out, p)
		}
	}
	return out
}
