package main

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// A routing point is a place the trip-keeper wants the car to drive through. It
// is not a stop: it is not a checkpoint, nothing checks in at it, and no mark,
// hour card or arrival reads it. Its one job is to be the place Navigate in
// Waze goes to next, because a Waze link holds one destination and no via
// points. It sits on a leg after a given checkpoint (After is that checkpoint's
// order) and before the next.
//
// Two sources: 'plan' (a Point feature with kind "routing" in the trip
// document, owned by the document) and 'road' (added by the trip-keeper while
// travelling, kept across a re-push).
type RoutingPoint struct {
	ID       int64   `json:"id"`
	LegID    int64   `json:"leg_id"`
	AfterOrd int     `json:"after"`
	Name     string  `json:"name"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	Source   string  `json:"source"`
}

const (
	routingFromPlan = "plan"
	routingFromRoad = "road"
	maxRoutingName  = 80
)

// ErrRoutingRefused is the add the guard turned away: the leg has been driven,
// or the place is private, or it lies nowhere on the leg. The text says which.
var ErrRoutingRefused = errors.New("routing point refused")

func insertPlanRouting(tx *sql.Tx, legIDs map[int]int64, rps []seedRP) error {
	for _, x := range rps {
		if _, err := tx.Exec(`INSERT INTO routing_points (leg_id,after_ord,name,lat,lng,source) VALUES (?,?,?,?,?,?)`,
			legIDs[x.LegOrd], x.After, x.Name, x.Lat, x.Lng, routingFromPlan); err != nil {
			return fmt.Errorf("routing point %q: %w", x.Name, err)
		}
	}
	return nil
}

// RoutingByLeg reads a leg's routing points in order along the leg. They are
// public places the trip-keeper chose, so they are read as they were stored;
// the private-place check is made when one is written (checkPlan and
// AddRoutingPoint), never on the way out.
func (s *Store) RoutingByLeg(legID int64) ([]RoutingPoint, error) {
	rows, err := s.db.Query(`SELECT id,leg_id,after_ord,name,lat,lng,source FROM routing_points
	  WHERE leg_id=? ORDER BY after_ord, id`, legID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoutingPoint
	for rows.Next() {
		var r RoutingPoint
		if err := rows.Scan(&r.ID, &r.LegID, &r.AfterOrd, &r.Name, &r.Lat, &r.Lng, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// routingRange is where a routing point on a leg may sit: after the last stop
// of the legs before it, up to but not including the last stop of this leg.
// ok is false for a leg with no stop to lie before.
func routingRange(cpOrdByLeg map[int][]int, legOrd int) (lo, hi int, ok bool) {
	for l, ords := range cpOrdByLeg {
		for _, o := range ords {
			if l < legOrd && o > lo {
				lo = o
			}
		}
	}
	own := cpOrdByLeg[legOrd]
	if len(own) == 0 {
		return 0, 0, false
	}
	for _, o := range own {
		if o > hi {
			hi = o
		}
	}
	return lo, hi - 1, hi-1 >= lo
}

// inPrivateZoneOf reports whether a place falls where a private (fuzzed)
// checkpoint's zone is drawn. A routing point is a name on a card and a link
// out, so it may not be at or beside a private place.
func inPrivateZoneOf(lat, lng float64, name string, privates []Checkpoint) bool {
	for _, c := range privates {
		if inPrivateZone(lat, lng, c) {
			return true
		}
		if c.Name != "" && name != "" && strings.Contains(strings.ToLower(name), strings.ToLower(c.Name)) {
			return true
		}
	}
	return false
}

// checkRouting is the document-side half of the rules: every routing point is
// on a leg of the plan, sits where a routing point may sit, is a real
// coordinate and is not at a private place. It runs under every choice of
// every option, as checkPlan does.
func checkRouting(sd *seedData) error {
	if len(sd.Routing) == 0 {
		return nil
	}
	byLeg := map[int][]int{}
	var privates []Checkpoint
	for _, c := range sd.Checkpoints {
		byLeg[c.LegOrd] = append(byLeg[c.LegOrd], c.Ord)
		if c.Precision == precisionFuzzed {
			privates = append(privates, Checkpoint{Name: c.Name, Lat: snap(c.Lat), Lng: snap(c.Lng)})
		}
	}
	for _, x := range sd.Routing {
		if math.Abs(x.Lng) > 180 || math.Abs(x.Lat) > 90 || (x.Lat == 0 && x.Lng == 0) {
			return fmt.Errorf("a routing point on leg %d is not at a [lng, lat]", x.LegOrd)
		}
		if len(x.Name) > maxRoutingName {
			return fmt.Errorf("a routing point on leg %d has a name over %d characters", x.LegOrd, maxRoutingName)
		}
		lo, hi, ok := routingRange(byLeg, x.LegOrd)
		if !ok || x.After < lo || x.After > hi {
			return fmt.Errorf("a routing point on leg %d must follow a stop of that leg (or the one it starts from) and come before its last stop", x.LegOrd)
		}
		if inPrivateZoneOf(x.Lat, x.Lng, x.Name, privates) {
			return fmt.Errorf("a routing point on leg %d is at a private place; a routing point is a public place", x.LegOrd)
		}
	}
	return nil
}

// drivenLegs are the legs the car has been on: one with a post, or with a stop
// arrived at or skipped. What is driven is not rewritten.
func drivenLegs(tx *sql.Tx, rideSlug string) (map[int]bool, error) {
	rows, err := tx.Query(`SELECT l.ord FROM legs l WHERE l.ride_slug=? AND (
	    EXISTS (SELECT 1 FROM posts p WHERE p.leg_id=l.id)
	 OR EXISTS (SELECT 1 FROM checkpoints c WHERE c.leg_id=l.id AND (c.arrived_at<>'' OR c.skipped_at<>'')))`, rideSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var o int
		if err := rows.Scan(&o); err != nil {
			return nil, err
		}
		out[o] = true
	}
	return out, rows.Err()
}

type rpKey struct {
	after    int
	name     string
	lat, lng float64
}

// routingConflicts is planConflicts' routing half: a plan may not change the
// routing points the document owns on a leg that has been driven. It appends
// to the reasons the other half found.
func routingConflicts(tx *sql.Tx, sd *seedData, reasons []string) ([]string, error) {
	driven, err := drivenLegs(tx, sd.Ride.Slug)
	if err != nil {
		return nil, err
	}
	if len(driven) == 0 {
		return reasons, nil
	}
	have := map[int]map[rpKey]bool{}
	rows, err := tx.Query(`SELECT l.ord, r.after_ord, r.name, r.lat, r.lng FROM routing_points r
	  JOIN legs l ON l.id=r.leg_id WHERE l.ride_slug=? AND r.source='plan'`, sd.Ride.Slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var o int
		var k rpKey
		if err := rows.Scan(&o, &k.after, &k.name, &k.lat, &k.lng); err != nil {
			return nil, err
		}
		if have[o] == nil {
			have[o] = map[rpKey]bool{}
		}
		have[o][k] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	want := map[int]map[rpKey]bool{}
	for _, x := range sd.Routing {
		if want[x.LegOrd] == nil {
			want[x.LegOrd] = map[rpKey]bool{}
		}
		want[x.LegOrd][rpKey{x.After, x.Name, x.Lat, x.Lng}] = true
	}
	var legs []int
	for o := range driven {
		legs = append(legs, o)
	}
	sort.Ints(legs)
	for _, o := range legs {
		same := len(have[o]) == len(want[o])
		for k := range have[o] {
			if !want[o][k] {
				same = false
			}
		}
		if !same {
			reasons = append(reasons, fmt.Sprintf("leg %d has been driven and the new plan changes its routing points", o))
		}
	}
	return reasons, nil
}

// AddRoutingPoint is the trip-keeper adding a routing point on the road, to a
// leg ahead. The same rules as the document: it sits where a routing point may
// sit, is public, and the leg has not been driven (no post, nothing arrived at
// or skipped). It adds a routing_points row and touches nothing else: no stop,
// no post, no leg.
func (s *Store) AddRoutingPoint(rideSlug, legSlug string, after int, name string, lat, lng float64) (*RoutingPoint, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxRoutingName {
		return nil, fmt.Errorf("%w: a routing point needs a name of up to %d characters", ErrRoutingRefused, maxRoutingName)
	}
	if math.IsNaN(lat) || math.IsNaN(lng) || math.Abs(lat) > 90 || math.Abs(lng) > 180 || (lat == 0 && lng == 0) {
		return nil, fmt.Errorf("%w: that is not a place", ErrRoutingRefused)
	}
	legs, err := s.Legs(rideSlug)
	if err != nil {
		return nil, err
	}
	var leg *Leg
	byLeg := map[int][]int{}
	for i := range legs {
		for _, c := range legs[i].Checkpoints {
			byLeg[legs[i].Ord] = append(byLeg[legs[i].Ord], c.Ord)
		}
		if legs[i].Slug == legSlug {
			leg = &legs[i]
		}
	}
	if leg == nil {
		return nil, fmt.Errorf("%w: the ride has no leg %q", ErrRoutingRefused, legSlug)
	}
	lo, hi, ok := routingRange(byLeg, leg.Ord)
	if !ok || after < lo || after > hi {
		return nil, fmt.Errorf("%w: a routing point must follow a stop of that leg (or the one it starts from) and come before its last stop", ErrRoutingRefused)
	}
	if inPrivateZoneOf(lat, lng, name, fuzzedOf(rideCheckpoints(legs))) {
		return nil, fmt.Errorf("%w: that is at a private place", ErrRoutingRefused)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	driven, err := drivenLegs(tx, rideSlug)
	if err != nil {
		return nil, err
	}
	if driven[leg.Ord] {
		return nil, fmt.Errorf("%w: leg %d has been driven and the road already driven does not change", ErrRoutingRefused, leg.Ord)
	}
	res, err := tx.Exec(`INSERT INTO routing_points (leg_id,after_ord,name,lat,lng,source) VALUES (?,?,?,?,?,?)`,
		leg.ID, after, name, lat, lng, routingFromRoad)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &RoutingPoint{ID: id, LegID: leg.ID, AfterOrd: after, Name: name, Lat: lat, Lng: lng, Source: routingFromRoad}, nil
}

// nextRoutingPoint is the first routing point on a leg the car has not passed
// and that lies before the next stop. A point is passed once a stop after it is
// arrived at or skipped, or once the car's progress along the leg is at it. The
// caller gives the next stop's order, each stop's state, and (when it has one)
// the car's progress and each point's own progress along the leg.
func nextRoutingPoint(leg *Leg, nextOrd int, alongOf func(RoutingPoint) (float64, bool), posAlong float64, haveAlong bool) *RoutingPoint {
	for i := range leg.RoutingPoints {
		rp := leg.RoutingPoints[i]
		if rp.AfterOrd >= nextOrd {
			continue // after the next stop: a later one's business
		}
		passed := false
		for _, c := range leg.Checkpoints {
			if c.Ord > rp.AfterOrd && (c.ArrivedAt != "" || c.SkippedAt != "") {
				passed = true
			}
		}
		if !passed && haveAlong {
			if a, ok := alongOf(rp); ok && a <= posAlong+0.5 {
				passed = true
			}
		}
		if !passed {
			return &leg.RoutingPoints[i]
		}
	}
	return nil
}
