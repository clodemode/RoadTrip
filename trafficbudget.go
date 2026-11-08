package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// The Traffic chips on a planned leg are a short list of what is ahead, not
// everything TomTom's wide boxes happened to return. An incident gets a chip
// only when its geometry comes within tripRouteKm of the leg's line and it is
// not behind the car; the nearest tripMaxAhead show, closest first, with how
// far, and the rest are one summary chip. An incident with no usable geometry
// is treated as on the route with an unknown distance, ranked after the
// measured ones: hiding a real closure is the worse failure.
//
// TomTom is asked once per cache window per leg. The fetched incidents are
// cached, and which of them are ahead is worked out again for every request,
// because the car moves and the cache lasts ten minutes. Aimless and Destined
// have no leg line and go through ForLeg unchanged.

const (
	tripRouteKm  = 1.0 // an incident this close to the line is on the route
	tripBehindKm = 0.5 // an incident this far behind the car still counts as at it
	tripMaxAhead = 3
)

// tripInc is one major incident, placed against the leg's line once, when it
// was fetched.
type tripInc struct {
	text    string
	weight  int
	onRoute bool
	placed  bool    // false: no usable geometry, distance unknown
	startKm float64 // along the line, nearest end of the incident
	endKm   float64 // along the line, far end of the incident
}

type tripEntry struct {
	at      time.Time
	ttl     time.Duration
	weather []hazardChip
	state   string // "", "nokey" or "noroute"
	incs    int    // incidents TomTom returned, before the minor ones are set aside
	minor   int
	major   []tripInc
	failed  int
	err     error
}

// ForTrip returns the weather chips and the budgeted traffic chips for one
// planned leg. line is the leg's polyline and aheadKm is the car's distance
// along it, nil when the HUD has no position (distances then run from the
// start of the leg). A line that cannot be measured falls back to ForLeg.
func (s *hazardSvc) ForTrip(ctx context.Context, key string, pts []ll, line json.RawMessage, aheadKm *float64) []hazardChip {
	if _, total, _, ok := alongLine(line, 0, 0); !ok || total <= 0 {
		return s.ForLeg(ctx, key, pts)
	}
	s.mu.Lock()
	e, ok := s.trips["trip:"+key]
	fresh := ok && s.now().Sub(e.at) < e.ttl
	s.mu.Unlock()
	if !fresh {
		e = s.fetchTrip(ctx, pts, line)
		e.at = s.now()
		e.ttl = hazardCacheTTL
		if e.failed > 0 || e.state != "" {
			e.ttl = hazardUnknownTTL
		}
		for _, c := range e.weather {
			if c.State == hazardUnknown {
				e.ttl = hazardUnknownTTL
			}
		}
		s.mu.Lock()
		if s.trips == nil {
			s.trips = map[string]tripEntry{}
		}
		s.trips["trip:"+key] = e
		s.mu.Unlock()
	}
	ahead := 0.0
	if aheadKm != nil {
		ahead = *aheadKm
	}
	out := append([]hazardChip(nil), e.weather...)
	return append(out, tripTraffic(e, ahead)...)
}

func (s *hazardSvc) fetchTrip(ctx context.Context, pts []ll, line json.RawMessage) tripEntry {
	var e tripEntry
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); e.weather = s.Weather(ctx, pts) }()
	go func() {
		defer wg.Done()
		switch {
		case s.key == "":
			e.state = "nokey"
		case len(pts) == 0:
			e.state = "noroute"
		default:
			var incs []ttIncident
			incs, e.failed, e.err = s.fetchIncidents(ctx, pts, true)
			seen := map[string]bool{}
			for _, in := range incs {
				if in.id != "" {
					if seen[in.id] {
						continue
					}
					seen[in.id] = true
				}
				e.incs++
				if in.mag < 2 && in.cat != 8 {
					e.minor++
					continue
				}
				e.major = append(e.major, placeIncident(in, line))
			}
		}
	}()
	wg.Wait()
	return e
}

// placeIncident measures one incident against the leg's line. Every vertex of
// its geometry within tripRouteKm of the line counts; the incident spans the
// nearest and the farthest of them along the line.
func placeIncident(in ttIncident, line json.RawMessage) tripInc {
	t := tripInc{text: in.text, weight: in.mag}
	if in.cat == 8 || in.mag == 4 {
		t.weight = 100
	}
	var verts [][]float64
	switch in.geom.Type {
	case "Point":
		var p []float64
		if json.Unmarshal(in.geom.Coords, &p) == nil && len(p) >= 2 {
			verts = [][]float64{p}
		}
	case "LineString":
		_ = json.Unmarshal(in.geom.Coords, &verts)
	}
	if len(verts) == 0 {
		t.onRoute = true // no usable geometry: shown, distance unknown
		return t
	}
	t.placed = true
	for _, v := range verts {
		if len(v) < 2 {
			continue
		}
		along, _, off, ok := alongLine(line, v[1], v[0])
		if !ok || off > tripRouteKm {
			continue
		}
		if !t.onRoute || along < t.startKm {
			t.startKm = along
		}
		if !t.onRoute || along > t.endKm {
			t.endKm = along
		}
		t.onRoute = true
	}
	return t
}

// tripTraffic turns the cached incidents into this request's chips.
func tripTraffic(e tripEntry, aheadKm float64) []hazardChip {
	switch e.state {
	case "nokey":
		return unknownChip("Traffic", "no key")
	case "noroute":
		return unknownChip("Traffic", "no route")
	}
	if e.incs == 0 {
		if e.failed > 0 {
			return unknownChip("Traffic", shortWhy(e.err))
		}
		return []hazardChip{{Source: "Traffic", State: hazardClear, Text: "Traffic: no incidents"}}
	}
	if len(e.major) == 0 {
		return []hazardChip{{Source: "Traffic", State: hazardClear, Text: fmt.Sprintf("Traffic: no major incidents (%d minor)", e.minor)}}
	}
	var ahead []tripInc
	off, behind := 0, 0
	for _, in := range e.major {
		switch {
		case !in.onRoute:
			off++
		case in.placed && in.endKm < aheadKm-tripBehindKm:
			behind++
		default:
			ahead = append(ahead, in)
		}
	}
	// Nearest first; one with no measured distance after every measured one;
	// a closure before a delay only when they are equally near.
	sort.SliceStable(ahead, func(i, j int) bool {
		a, b := ahead[i], ahead[j]
		if a.placed != b.placed {
			return a.placed
		}
		if a.placed && a.startKm != b.startKm {
			return a.startKm < b.startKm
		}
		return a.weight > b.weight
	})
	miles := func(in tripInc) string {
		d := in.startKm - aheadKm
		if d < 0 {
			d = 0
		}
		return hudMiles(d * kmToMi)
	}
	var chips []hazardChip
	if len(ahead) == 0 {
		text := "Traffic: nothing on the route"
		if behind > 0 {
			text = "Traffic: nothing ahead on the route"
		}
		if off > 0 {
			text += fmt.Sprintf(" (%d nearby off route)", off)
		}
		chips = append(chips, hazardChip{Source: "Traffic", State: hazardClear, Text: text})
	}
	for i, in := range ahead {
		if i == tripMaxAhead {
			text := fmt.Sprintf("Traffic: %d more ahead", len(ahead)-i)
			if in.placed {
				text += ", next at " + miles(in)
			}
			chips = append(chips, hazardChip{Source: "Traffic", State: hazardItems, Text: text})
			break
		}
		text := "Traffic: " + in.text
		if in.placed {
			text = "Traffic: " + miles(in) + " ahead, " + in.text
		}
		chips = append(chips, hazardChip{Source: "Traffic", State: hazardItems, Text: text})
	}
	if e.failed > 0 {
		chips = append(chips, hazardChip{Source: "Traffic", State: hazardUnknown, Text: "Traffic: some of the route could not be checked"})
	}
	return chips
}
