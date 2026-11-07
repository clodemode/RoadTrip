package main

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The simulated drive (the Trailer Test), held on the server.
//
// One position per signed-in user, computed at request time from the
// corridor, the seed, the start instant and the compression. Nothing ticks:
// no tab, no timer. Nothing is stored in a browser, and the position is never
// sent to one: it is consumed here (the HUD, Chester) the way a phone fix is.
// Every page and every device signed in as the user sees the same car at the
// same instant because they all ask this one answer. It is in memory, a
// restart forgets it, and it exists only where SIM_ALLOWED=1 (a KATE, never
// Fly). The corridor, the PRNG and the speed and jitter laws are a port of
// the pure helpers in static/js/sim.js, so a seed means the same drive in both.

const (
	simRMi    = 3958.7613
	simRad    = math.Pi / 180
	simCorKGN = "kgn-yyz"
)

var simCorridors = map[string][][2]float64{
	simCorKGN: {{44.2290, -76.4810}, {44.2615, -76.5600}, {44.2800, -76.7200}, {44.2600, -76.9500},
		{44.1900, -77.3800}, {44.1150, -77.5800}, {44.0500, -77.7400}, {44.0000, -77.9000},
		{43.9700, -78.1700}, {43.9550, -78.2950}, {43.9200, -78.5900}, {43.9150, -78.6900},
		{43.9000, -78.8600}, {43.8900, -78.9400}, {43.8600, -79.0300}, {43.8450, -79.0900},
		{43.7900, -79.2500}, {43.6532, -79.3832}},
}

var simLabels = map[string]string{simCorKGN: "Kingston→Toronto corridor"}

type simState struct {
	Corridor string
	Seed     uint32
	T0       time.Time
	X        float64 // time compression
	MphCap   float64 // 0 = the seeded 55-75 mph
	RungMi   float64 // 0 = the whole corridor
}

type simSvc struct {
	mu     sync.Mutex
	byUser map[string]simState
	now    func() time.Time
}

func newSimSvc() *simSvc { return &simSvc{byUser: map[string]simState{}, now: time.Now} }

func (a *App) simSvc() *simSvc {
	if a.sim == nil {
		a.sim = newSimSvc()
	}
	return a.sim
}

func (s *simSvc) Start(user string, st simState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byUser[user] = st
}

func (s *simSvc) Stop(user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byUser, user)
}

func (s *simSvc) State(user string) (simState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.byUser[user]
	return st, ok
}

// PositionAt is the simulated car for user at now; ok is false when no drive
// runs for that user.
func (s *simSvc) PositionAt(user string, now time.Time) (lat, lng, heading, mph, milesDone float64, ok bool) {
	st, ok := s.State(user)
	if !ok {
		return 0, 0, 0, 0, 0, false
	}
	elapsed := math.Max(0, now.Sub(st.T0).Seconds())
	w := simWalk(st, elapsed)
	return w.pos[0], w.pos[1], w.heading, w.mph, w.d, true
}

func simHaversine(la1, lo1, la2, lo2 float64) float64 {
	dLa, dLo := (la2-la1)*simRad, (lo2-lo1)*simRad
	a := math.Sin(dLa/2)*math.Sin(dLa/2) + math.Cos(la1*simRad)*math.Cos(la2*simRad)*math.Sin(dLo/2)*math.Sin(dLo/2)
	return 2 * simRMi * math.Asin(math.Min(1, math.Sqrt(a)))
}

func simBearing(la1, lo1, la2, lo2 float64) float64 {
	dLo := (lo2 - lo1) * simRad
	y := math.Sin(dLo) * math.Cos(la2*simRad)
	x := math.Cos(la1*simRad)*math.Sin(la2*simRad) - math.Sin(la1*simRad)*math.Cos(la2*simRad)*math.Cos(dLo)
	return math.Mod(math.Atan2(y, x)/simRad+360, 360)
}

// simPointAt walks d miles from (la1,lo1) toward (la2,lo2), never past it.
func simPointAt(la1, lo1, la2, lo2, d float64) [2]float64 {
	total := simHaversine(la1, lo1, la2, lo2)
	if total == 0 || d >= total {
		return [2]float64{la2, lo2}
	}
	dr, th, p1, l1 := d/simRMi, simBearing(la1, lo1, la2, lo2)*simRad, la1*simRad, lo1*simRad
	p2 := math.Asin(math.Sin(p1)*math.Cos(dr) + math.Cos(p1)*math.Sin(dr)*math.Cos(th))
	l2 := l1 + math.Atan2(math.Sin(th)*math.Sin(dr)*math.Cos(p1), math.Cos(dr)-math.Sin(p1)*math.Sin(p2))
	return [2]float64{p2 / simRad, math.Mod(math.Mod(l2/simRad+540, 360)+360, 360) - 180}
}

func simPathLength(pts [][2]float64) float64 {
	t := 0.0
	for i := 1; i < len(pts); i++ {
		t += simHaversine(pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1])
	}
	return t
}

// simWalkPath: the point d miles along the polyline and the segment's bearing.
func simWalkPath(pts [][2]float64, d float64) ([2]float64, float64) {
	left, n := math.Max(0, d), len(pts)
	for i := 1; i < n; i++ {
		a, b := pts[i-1], pts[i]
		seg := simHaversine(a[0], a[1], b[0], b[1])
		if left <= seg || i == n-1 {
			var pos [2]float64
			switch {
			case left >= seg-1e-9:
				pos = b
			case left == 0:
				pos = a
			default:
				pos = simPointAt(a[0], a[1], b[0], b[1], left)
			}
			return pos, simBearing(a[0], a[1], b[0], b[1])
		}
		left -= seg
	}
	return pts[0], 0
}

// simPRNG is mulberry32, [0,1).
func simPRNG(seed uint32) func() float64 {
	a := seed
	return func() float64 {
		a += 0x6D2B79F5
		t := a
		t = (t ^ (t >> 15)) * (t | 1)
		t ^= t + (t^(t>>7))*(t|61)
		return float64(t^(t>>14)) / 4294967296
	}
}

func simMix(seed uint32, k int) uint32 { return seed ^ uint32(int32(k+1))*0x9E3779B1 }

// simSpeedAt: mph at simulated second t, re-drawn each 60 s, 55-75, capped.
func simSpeedAt(seed uint32, t, capMph float64) float64 {
	v := 55 + 20*simPRNG(simMix(seed, int(math.Floor(t/60))))()
	if capMph > 0 {
		return math.Min(v, capMph)
	}
	return v
}

func simMilesAt(seed uint32, simSec, capMph float64) float64 {
	mi, m := 0.0, 0
	for ; float64(m+1)*60 <= simSec; m++ {
		mi += simSpeedAt(seed, float64(m)*60, capMph) * 60 / 3600
	}
	return mi + simSpeedAt(seed, float64(m)*60, capMph)*(simSec-float64(m)*60)/3600
}

func simStartOffset(seed uint32) float64 { return 5 * simPRNG(simMix(seed, -1))() }

// simJitter: up to 30 m sideways of the segment.
func simJitter(pos [2]float64, heading float64, seed uint32, k int) [2]float64 {
	r := simPRNG(simMix(seed, 100000+k))
	side := (r()*2 - 1) * 29.9
	th := (heading + 90) * simRad
	dn, de := side*math.Cos(th), side*math.Sin(th)
	return [2]float64{pos[0] + dn/111320, pos[1] + de/(111320*math.Cos(pos[0]*simRad))}
}

type simStep struct {
	pos          [2]float64
	heading, mph float64
	d            float64
	done         bool
}

func simWalk(st simState, elapsed float64) simStep {
	pts := simCorridors[st.Corridor]
	total := simPathLength(pts)
	start := math.Min(simStartOffset(st.Seed), total)
	rung := st.RungMi
	if rung <= 0 {
		rung = total
	}
	capMi := math.Min(rung, total-start)
	simSec := elapsed * st.X
	d := math.Min(capMi, simMilesAt(st.Seed, simSec, st.MphCap))
	done := d >= capMi
	base, heading := simWalkPath(pts, start+d)
	pos := base
	if !done {
		pos = simJitter(base, heading, st.Seed, int(math.Floor(elapsed)))
	}
	return simStep{pos: pos, heading: heading, d: d, done: done, mph: simSpeedAt(st.Seed, simSec, st.MphCap)}
}

// simFix is the position a request would otherwise have asked the phone for.
func (a *App) simFix(r *http.Request) (lat, lng *float64) {
	if !a.simAllowed || a.sim == nil {
		return nil, nil
	}
	la, ln, _, _, _, ok := a.sim.PositionAt(a.userOf(r), a.sim.now())
	if !ok {
		return nil, nil
	}
	return &la, &ln
}

// simBanner is the sticky banner's text, or "" when no drive runs for the user.
// Computed at render; nothing in the page ticks.
func (a *App) simBanner(r *http.Request) string {
	if !a.simAllowed || a.sim == nil {
		return ""
	}
	user := a.userOf(r)
	st, ok := a.sim.State(user)
	if !ok {
		return ""
	}
	_, _, _, mph, done, _ := a.sim.PositionAt(user, a.sim.now())
	return fmt.Sprintf("SIMULATED GPS — %s, seed %d, %.1f mi at %d mph x%s", simLabels[st.Corridor], st.Seed, done,
		int(math.Round(mph)), strconv.FormatFloat(st.X, 'f', -1, 64))
}

func (a *App) simStart(w http.ResponseWriter, r *http.Request) {
	if !a.simAllowed {
		http.NotFound(w, r)
		return
	}
	corridor := strings.TrimSpace(r.FormValue("corridor"))
	if corridor == "" {
		corridor = simCorKGN
	}
	if _, ok := simCorridors[corridor]; !ok {
		http.Error(w, "unknown corridor", http.StatusBadRequest)
		return
	}
	seed := uint32(time.Now().UnixNano())
	if s := strings.TrimSpace(r.FormValue("seed")); s != "" {
		v, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			http.Error(w, "seed must be a whole number", http.StatusBadRequest)
			return
		}
		seed = uint32(v)
	}
	st := simState{Corridor: corridor, Seed: seed, T0: a.simSvc().now(),
		X: simPositive(r.FormValue("x"), 60), MphCap: simPositive(r.FormValue("mph"), 0), RungMi: simPositive(r.FormValue("rung"), 0)}
	if st.X > 3600 {
		st.X = 3600
	}
	a.simSvc().Start(a.userOf(r), st)
	http.Redirect(w, r, "/driver", http.StatusSeeOther)
}

func (a *App) simStop(w http.ResponseWriter, r *http.Request) {
	if a.sim != nil {
		a.sim.Stop(a.userOf(r))
	}
	http.Redirect(w, r, "/driver", http.StatusSeeOther)
}

// simJSON says whether a drive runs and how far along it is. Never the position.
func (a *App) simJSON(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"running": false}
	if a.simAllowed && a.sim != nil {
		user := a.userOf(r)
		if st, ok := a.sim.State(user); ok {
			_, _, _, mph, done, _ := a.sim.PositionAt(user, a.sim.now())
			out = map[string]any{"running": true, "corridor": st.Corridor, "seed": st.Seed,
				"miles_done": math.Round(done*10) / 10, "mph": math.Round(mph), "started_at": st.T0.UTC().Format(time.RFC3339)}
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, out)
}

func simPositive(s string, dflt float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && v > 0 {
		return v
	}
	return dflt
}
