package main

import "net/http"

// navKind is how a stop is navigated to. Kris, 2026-11-18: "Must use precise
// addresses for navigation." A stop is a destination and needs a precise
// address; a link built from a guessed or town-level point is the Manor defect
// (a stop's point was a guess, its address was on file, Navigate went to the
// guess). So every stop declares how it is navigated, and a stop that declares
// nothing is navigated by nothing.
type navKind string

const (
	// navAddress: the trip-keeper's private overlay holds an address for the
	// stop. The links go to it, as search text.
	navAddress navKind = "address"
	// navPoint: the trip document says "nav": "point" on the stop, a
	// trailhead or a border crossing with no street address, whose point is
	// exact. The links go to the point (a fuzzed place, to its name).
	navPoint navKind = "point"
	// navPlace: the stop is one of the ride owner's saved places ("place":
	// "home" in the trip document, profile.go). The links go to the place's
	// address, which only the signed-in driver is handed.
	navPlace navKind = "place"
	// navNone: none of these. There is no link, and a trip does not go live
	// with it.
	navNone navKind = "none"
)

// navTarget is the ONE decision of how a stop is navigated: the link
// (stopNav), its label, the readiness page and the push's check all ask it, so
// the link and the check can never disagree. addr is navAddresses' map, nil
// for anyone who is not the trip-keeper.
func navTarget(c Checkpoint, addr map[int]string) navKind {
	switch {
	case c.Place != "" && addr[c.Ord] != "":
		return navPlace
	case c.Place != "":
		// A place stop goes to its place or nowhere: never a guess, never an
		// overlay entry filed under its number.
		return navNone
	case addr[c.Ord] != "":
		return navAddress
	case c.NavPoint:
		return navPoint
	}
	return navNone
}

// stopNav is the ONE place a navigate link to a checkpoint is built: the
// driver HUD, the Ride and leg pages' stop rows and the day's end all call it.
// A stop with an address goes to that address, as search text, private or not.
// A navigate-by-point stop goes to its coordinates, a fuzzed one to its name.
// A stop with neither gets no links at all, never a guess. A routing point
// (routing.go) is a swing-past point, never a stop, and keeps its own
// approximate point. addr comes from navAddresses and is nil for anyone who is
// not the signed-in trip-keeper, so no visitor's response is built from an
// address.
func stopNav(c Checkpoint, addr map[int]string) navLinks {
	switch navTarget(c, addr) {
	case navAddress, navPlace:
		return mapLinks(0, 0, addr[c.Ord])
	case navPoint:
		if c.Precision == precisionFuzzed {
			return mapLinks(0, 0, c.Name)
		}
		return mapLinks(c.Lat, c.Lng, c.Name)
	}
	return navLinks{}
}

// stopNavLabel is what a Navigate button says it goes to, in the words of
// what stopNav's links carry (Kris, 2026-11-18: the button must show the
// destination). Empty for a stop with no target: there is no button.
func stopNavLabel(c Checkpoint, addr map[int]string) string {
	switch navTarget(c, addr) {
	case navAddress, navPlace:
		return addr[c.Ord]
	case navPoint:
		if c.Precision == precisionFuzzed {
			return "a search for the name " + c.Name
		}
		return "the map point for " + c.Name
	}
	return ""
}

// viaNavLabel is the same for a routing point the links go to first.
func viaNavLabel(name string) string { return "the swing-past point " + name }

// stopNavText is the text a stop is navigated to when it has no coordinates
// to go by: its address when the trip-keeper has one on file, else its name.
func stopNavText(c Checkpoint, addr map[int]string) string {
	if a := addr[c.Ord]; a != "" {
		return a
	}
	return c.Name
}

// navAddresses is the active ride's private addresses by checkpoint ord, for
// a request that may see them: the signed-in trip-keeper, and only on the
// ride the site shows. Anyone else gets nil. An unreadable overlay also gives
// nil, so a stop reads as having no address; nothing of it is logged.
func (a *App) navAddresses(r *http.Request) map[int]string {
	if !a.isDriver(r) {
		return nil
	}
	pd, err := loadPrivate(a.dataDir)
	if err != nil {
		return nil
	}
	rv, err := a.loadRide()
	if err != nil {
		return nil
	}
	slug := a.activeSlug()
	return a.placeAddresses(slug, rv.Legs, pd.forRide(slug, a.rideSlug).onPlan(rv.Legs).addressByOrd())
}

// navNavigable reports whether every stop still to be driven to has a
// target, and names the ones that do not, in order. The start (where the car
// leaves from) and a settled stop (arrived at or passed) are never navigated
// to and are not asked. It is what the trip push, the activation and the
// readiness page share, so what they say can never differ from what the links
// do.
func navMissing(legs []Leg, addr map[int]string) []string {
	out := []string{}
	for _, l := range legs {
		for _, c := range l.Checkpoints {
			if c.IsStart || c.ArrivedAt != "" || c.SkippedAt != "" {
				continue
			}
			if navTarget(c, addr) == navNone {
				out = append(out, c.Name)
			}
		}
	}
	return out
}

// rideNavMissing is navMissing for a named ride, read as the trip-keeper
// would see it: its legs and its own block of the private overlay.
func (a *App) rideNavMissing(slug string) ([]string, error) {
	legs, err := a.store.Legs(slug)
	if err != nil {
		return nil, err
	}
	followArrivals(legs)
	markCheckpoints(legs)
	pd, err := loadPrivate(a.dataDir)
	if err != nil {
		// An overlay that does not parse gives no address, so no stop is
		// navigable; say so rather than guess.
		pd = privateData{}
	}
	return navMissing(legs, a.placeAddresses(slug, legs, pd.forRide(slug, a.rideSlug).onPlan(legs).addressByOrd())), nil
}
