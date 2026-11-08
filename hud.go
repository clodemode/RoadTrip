package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The driver screen: a portrait HUD for the phone in a mount. The centre is
// the next decision, the top edge is gauges, the sides are thumb tools and the
// bottom is the phone's own telemetry (kept in the browser, not here).
//
// Everything on it comes from the plan and the store, or from a position the
// phone sent for this one request and nobody keeps. A figure that cannot be
// known is a dash, never a zero.

const (
	kmToMi        = 0.621371
	hudOffRouteKm = 50.0 // a fix further than this from every leg is not believed
)

type hudView struct {
	Source   string `json:"source"` // "position" or "plan"
	DayN     int    `json:"day_n"`
	DayTotal int    `json:"day_total"`
	LegSlug  string `json:"leg_slug"`
	LegName  string `json:"leg_name"`

	HaveMiles  bool    `json:"have_miles"`
	MilesDoneN float64 `json:"miles_done_n"`
	MilesLeftN float64 `json:"miles_left_n"`
	MilesDone  string  `json:"miles_done"`
	MilesLeft  string  `json:"miles_left"`

	TonightName   string `json:"tonight_name"`
	ETA           string `json:"eta"`
	ETAPace       string `json:"eta_pace,omitempty"`
	OffRoute      bool   `json:"off_route,omitempty"`
	OffRouteMiles int    `json:"off_route_miles,omitempty"`
	TZ            string `json:"tz"`
	Crossing      string `json:"crossing"`

	// DriveStarted is true once the trip's start has been left. StartName is
	// the start's name, given only until then: the screen shows it as the
	// start, with no navigation to it.
	DriveStarted bool   `json:"drive_started"`
	StartName    string `json:"start_name,omitempty"`

	HasNext   bool   `json:"has_next"`
	NextName  string `json:"next_name"`
	NextMiles string `json:"next_miles"`
	// NextLat/NextLng are the next stop's position, only for a precise place
	// (a private one is a snapped cell and never leaves the server). The
	// simulated drive uses them as its default destination.
	NextLat *float64 `json:"next_lat,omitempty"`
	NextLng *float64 `json:"next_lng,omitempty"`
	Waze    string   `json:"waze"`
	Google  string   `json:"google"`
	Apple   string   `json:"apple"`

	Hazards []hazardChip `json:"hazards,omitempty"`

	// What the ETA is a promise about: the stop and the time, for the ledger.
	// Never sent to the phone.
	etaCP int64
	etaAt time.Time

	// Free is set instead of the trip fields being believed when the signed-in
	// user is on a FreeLance drive.
	Free *freeView `json:"free,omitempty"`

	// Tracks says whether the RoadTracks card is on; nothing else about it.
	Tracks *tracksView `json:"tracks,omitempty"`
}

func hudMiles(mi float64) string { return strconv.Itoa(int(math.Round(mi))) + " mi" }

// buildHUD is pure: the ride, the clock and, optionally, a position.
func buildHUD(ride *Ride, legs []Leg, now time.Time, lat, lng *float64, moving bool) hudView {
	h := hudView{Source: "plan", MilesDone: "—", MilesLeft: "—", ETA: "—", NextMiles: "—"}
	if len(legs) == 0 {
		return h
	}
	h.DayTotal = len(legs)

	// Which leg: the one whose line the fix is on, else today's by date.
	li, posAlong, onLine, offRoute := -1, 0.0, false, false
	if lat != nil && lng != nil {
		best, nearest := math.Inf(1), math.Inf(1)
		for i := range legs {
			along, _, off, ok := alongLine(legs[i].Line, *lat, *lng)
			if ok && off < nearest {
				nearest = off
			}
			if ok && off < best && off < hudOffRouteKm {
				best, li, posAlong = off, i, along
			}
		}
		onLine = li >= 0
		// A fix far from every leg is said to be off the plan, and the plan's
		// own figures are not drawn from it. The distance is worked out here;
		// the fix goes nowhere.
		if !onLine && !math.IsInf(nearest, 1) {
			offRoute = true
			h.OffRoute, h.OffRouteMiles = true, int(math.Round(nearest*kmToMi))
		}
	}
	di := dayOf(ride, legs, now)
	if onLine {
		h.Source = "position"
	} else {
		li = 0
		if leg := defaultLeg(legs, di); leg != nil {
			for i := range legs {
				if legs[i].ID == leg.ID {
					li = i
				}
			}
		}
	}
	leg := &legs[li]
	h.DayN, h.LegSlug, h.LegName = li+1, leg.Slug, leg.Name
	if leg.Name == "" {
		h.LegName = leg.Slug
	}

	_, totalKm, _, haveLine := alongLine(leg.Line, 0, 0)
	cps := leg.Checkpoints
	alongs := make([]float64, len(cps))
	for i, c := range cps {
		if a, _, _, ok := alongLine(leg.Line, c.Lat, c.Lng); ok {
			alongs[i] = a
		}
	}
	// Without a fix, the furthest arrival on this leg stands in for "where we are".
	haveBase := onLine
	if !onLine && !offRoute && haveLine {
		for i, c := range cps {
			if c.ArrivedAt != "" && alongs[i] >= posAlong {
				posAlong, haveBase = alongs[i], true
			}
		}
	}
	if haveLine && haveBase {
		h.HaveMiles = true
		h.MilesDoneN, h.MilesLeftN = posAlong*kmToMi, math.Max(0, (totalKm-posAlong)*kmToMi)
		h.MilesDone, h.MilesLeft = hudMiles(h.MilesDoneN), hudMiles(h.MilesLeftN)
	} else if leg.EstMiles > 0 {
		h.MilesLeft = hudMiles(leg.EstMiles) + " planned"
	}

	// Tonight's stop, and the zone the clock is in now.
	var end *Checkpoint
	if n := len(cps); n > 0 {
		end = &cps[n-1]
		h.TonightName = end.Name
	}
	curTZ := defaultTZ(legs, leg)
	if onLine {
		best := math.Inf(1)
		for _, c := range rideCheckpoints(legs) {
			if km := haversineKm(*lat, *lng, c.Lat, c.Lng); km < best && c.TZ != "" {
				best, curTZ = km, c.TZ
			}
		}
	}
	h.TZ = now.In(loadLoc(curTZ)).Format("MST")

	// ETA at plan pace, only while the car is moving and we know where it is:
	// a promise made to a parked car, or from a guess at today's leg, is not
	// one to make.
	if moving && onLine && end != nil && leg.EstHours > 0 && haveLine && totalKm > 0 {
		leftFrac := math.Max(0, (totalKm-posAlong)/totalKm)
		eta := now.Add(time.Duration(leg.EstHours * leftFrac * float64(time.Hour)))
		h.ETA, h.ETAPace = aboutClock(eta, loadLoc(end.TZ)), "plan pace"
		h.etaCP, h.etaAt = end.ID, eta
	}

	// The next stop: the first checkpoint ahead that nobody has arrived at.
	if st := tripStart(legs); st != nil {
		h.DriveStarted = st.ArrivedAt != ""
		if !h.DriveStarted {
			h.StartName = st.Name
		}
	}
	startID := int64(-1)
	if st := tripStart(legs); st != nil {
		startID = st.ID
	}
	nextIdx := -1
	for i, c := range cps {
		if c.ID == startID || c.ArrivedAt != "" || c.SkippedAt != "" || (haveLine && alongs[i] <= posAlong+0.5) {
			continue
		}
		nextIdx = i
		break
	}
	if nextIdx >= 0 {
		c := cps[nextIdx]
		h.HasNext, h.NextName = true, c.Name
		if haveLine && haveBase {
			h.NextMiles = hudMiles((alongs[nextIdx] - posAlong) * kmToMi)
		}
		// A private place is a snapped cell; Waze gets its name, never the cell.
		if c.Precision == precisionFuzzed {
			nav := mapLinks(0, 0, c.Name)
			h.Waze, h.Google, h.Apple = nav.Waze, nav.Google, nav.Apple
		} else {
			nav := mapLinks(c.Lat, c.Lng, c.Name)
			h.Waze, h.Google, h.Apple = nav.Waze, nav.Google, nav.Apple
			la, ln := c.Lat, c.Lng
			h.NextLat, h.NextLng = &la, &ln
		}
		// Do the clocks change somewhere ahead on this leg?
		here := now.In(loadLoc(curTZ)).Format("MST")
		for i := nextIdx; i < len(cps); i++ {
			if cps[i].TZ == "" {
				continue
			}
			if there := now.In(loadLoc(cps[i].TZ)).Format("MST"); there != here {
				h.Crossing = fmt.Sprintf("Clocks change at %s: %s → %s", cps[i].Name, here, there)
				break
			}
		}
	}
	return h
}

// hudData loads the ride and builds the view for one request.
func (a *App) hudData(now time.Time, lat, lng *float64, moving bool) (hudView, []Leg, error) {
	rv, err := a.loadRide()
	if err != nil {
		return hudView{}, nil, err
	}
	return buildHUD(rv.Ride, rv.Legs, now, lat, lng, moving), rv.Legs, nil
}

func parseFix(r *http.Request) (lat, lng *float64) {
	la, e1 := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	ln, e2 := strconv.ParseFloat(r.URL.Query().Get("lng"), 64)
	if e1 != nil || e2 != nil || math.Abs(la) > 90 || math.Abs(ln) > 180 || math.IsNaN(la) || math.IsNaN(ln) {
		return nil, nil
	}
	return &la, &ln
}

// driverHome is where sign-in lands: the HUD, drawn from the plan alone so it
// is instant. The phone then asks /driver/hud.json for the live parts.
func (a *App) driverHome(w http.ResponseWriter, r *http.Request) {
	st := a.freeSvc().get(a.userOf(r))
	onRide := a.auth.mayRide(r, a.activeSlug(), a.activeSlug())
	aimless := st.Mode == modeAimless
	var h hudView
	// Aimless is not the plan: its screen is drawn without the ride's numbers.
	if onRide && !aimless {
		var err error
		if h, _, err = a.hudData(time.Now(), nil, nil, false); err != nil {
			http.Error(w, "the ride could not be read", http.StatusInternalServerError)
			return
		}
	}
	// A driver on no ride has only FreeLance Destined, which has no ride; the
	// screen is drawn that way, so the mode it polls for is set to match.
	if !onRide && st.Mode != modeDestined && !aimless {
		a.freeSvc().set(a.userOf(r), func(s *freeState) { s.enter(modeDestined) })
	}
	a.render(w, r, "driver.html", map[string]any{"Title": "Driver", "HUD": true, "H": h, "DriveStarted": a.driveStarted(),
		"TracksOn": a.tracks != nil, "TracksURL": a.tracks.playerURL(), "Destined": st.Mode == modeDestined || (!onRide && !aimless), "Aimless": aimless, "FreeLabel": st.Label, "FreeErr": st.Err, "FreeHave": st.Have})
}

// hudJSON is the live HUD: position-aware, with the hazard chips of the leg.
// The position is used for this answer and is neither stored nor logged.
func (a *App) hudJSON(w http.ResponseWriter, r *http.Request) {
	lat, lng := parseFix(r)
	// No position from the browser and a simulated drive running for this
	// driver: the server's own position stands in, as a phone fix would.
	simUsed := false
	// Moving is the phone's word (hud.js sends moving=1 from its speed or its
	// last two minutes of fixes) or, for the server-held drive, a speed above 0.
	moving := lat != nil && r.URL.Query().Get("moving") == "1"
	if lat == nil {
		if sl, sg := a.simFix(r); sl != nil {
			lat, lng, simUsed = sl, sg, true
			_, _, _, mph, _, _ := a.sim.PositionAt(a.userOf(r), a.sim.now())
			moving = mph > 0
		}
	}
	// The trip HUD is the ride's; a driver off the ride gets only FreeLance
	// Destined, with none of the ride's numbers.
	onRide := a.auth.mayRide(r, a.activeSlug(), a.activeSlug())
	mode := a.freeSvc().get(a.userOf(r)).Mode
	if !onRide && mode != modeDestined && mode != modeAimless {
		a.refuseRide(w, r)
		return
	}
	var h hudView
	var legs []Leg
	// Aimless never reads the plan, so nothing of it can show.
	if onRide && mode != modeAimless {
		var err error
		now := time.Now()
		if h, legs, err = a.hudData(now, lat, lng, moving); err != nil {
			http.Error(w, "the ride could not be read", http.StatusInternalServerError)
			return
		}
		// Every ETA shown is kept, to be scored when the stop is reached.
		if mode == modeTrip && h.etaCP != 0 {
			if _, err := a.store.RecordETA(a.activeSlug(), h.etaCP, a.userOf(r), now, h.etaAt, "plan"); err != nil {
				log.Printf("eta ledger: %v", err)
			}
		}
	}
	if a.tracks != nil {
		h.Tracks = &tracksView{Enabled: true}
	}
	hz := a.hazards
	if hz == nil {
		hz = newHazardSvc("")
	}
	if user := a.userOf(r); mode == modeAimless {
		h.Free = a.aimlessHUD(user, lat, lng)
		key := "free:" + user + "|aimless|nofix"
		if lat != nil && lng != nil {
			key = fmt.Sprintf("free:%s|aimless|%.1f,%.1f", user, *lat, *lng)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*hazardTimeout)
		defer cancel()
		h.Hazards = hz.ForLeg(ctx, key, a.aimlessPoints(lat, lng))
		a.hudServed(r, mode, h, lat != nil, simUsed)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h)
		return
	}
	if user := a.userOf(r); mode == modeDestined {
		st := a.freeSvc().get(user)
		fctx, fcancel := context.WithTimeout(r.Context(), 3*freeTimeout)
		defer fcancel()
		fv, fpts := a.freeHUD(fctx, user, st, lat, lng)
		h.Free = fv
		ctx, cancel := context.WithTimeout(r.Context(), 2*hazardTimeout)
		defer cancel()
		// A routed line can end at a private place's door; hazards are never
		// asked about points near one, as on a planned leg.
		fpts = dropNearFuzzed(fpts, fuzzedOf(rideCheckpoints(legs)))
		h.Hazards = hz.ForLeg(ctx, "free:"+user+"|"+strings.ToLower(st.Query), fpts)
		a.hudServed(r, mode, h, lat != nil, simUsed)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h)
		return
	}
	var pts []ll
	var line json.RawMessage
	for _, l := range legs {
		if l.Slug == h.LegSlug {
			pts = legSamples(l.Line, 4, fuzzedOf(rideCheckpoints(legs)))
			line = l.Line
		}
	}
	// How far along the leg the car is, when the HUD has a position (a phone
	// fix or the server-held sim); else the chips measure from the leg's start.
	var aheadKm *float64
	if lat != nil && lng != nil {
		if al, _, _, ok := alongLine(line, *lat, *lng); ok {
			aheadKm = &al
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*hazardTimeout)
	defer cancel()
	h.Hazards = hz.ForTrip(ctx, h.LegSlug, pts, line, aheadKm)
	a.hudServed(r, mode, h, lat != nil, simUsed)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h)
}

// hudServed is the one MELT line per HUD request (pos_source, not source: the
// sink stamps its own source on every record): what the screen was given,
// never where the car is.
func (a *App) hudServed(r *http.Request, mode string, h hudView, hasFix, sim bool) {
	source := "plan"
	if h.Source == "position" || (mode != modeTrip && hasFix) || sim {
		source = "fix"
	}
	miles := 0
	if h.HaveMiles {
		miles = int(math.Round(h.MilesLeftN))
	}
	a.melt.Event("hud_served", map[string]any{"mode": mode, "pos_source": source, "has_fix": hasFix,
		"has_next": h.HasNext, "next_name": h.NextName, "miles_left": miles,
		"eta_shown": h.ETA != "" && h.ETA != "—", "sim": sim})
}

// driveStarted is false only while the active trip's start has no arrival: the
// driver's Post tool then reads Start the drive.
func (a *App) driveStarted() bool {
	rv, err := a.loadRide()
	if err != nil {
		return true
	}
	c := tripStart(rv.Legs)
	return c == nil || c.ArrivedAt != ""
}
