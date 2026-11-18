package main

import "net/http"

// stopNav is the ONE place a navigate link to a checkpoint is built: the
// driver HUD, the Ride and leg pages' stop rows and the day's end all call it.
//
// A stop is a place the car drives to, so it is navigated to by its precise
// address (Kris, 2026-11-18: a stop's point was a guess near the town, its
// address was on file, and Navigate went to the guess). A stop with an
// address in addr goes to that address, as search text, private or not. With
// none, a private (fuzzed) stop is a snapped cell and goes to its name; any
// other stop goes to its coordinates. addr comes from navAddresses and is nil
// for anyone who is not the signed-in trip-keeper, so no visitor's response
// is built from an address. A routing point (routing.go) is a swing-past
// point, never a stop, and keeps its own approximate point.
func stopNav(c Checkpoint, addr map[int]string) navLinks {
	if a := addr[c.Ord]; a != "" {
		return mapLinks(0, 0, a)
	}
	if c.Precision == precisionFuzzed {
		return mapLinks(0, 0, c.Name)
	}
	return mapLinks(c.Lat, c.Lng, c.Name)
}

// stopNavLabel is what a Navigate button says it goes to, in the words of
// what stopNav's links carry (Kris, 2026-11-18: the button must show the
// destination): the address, a search for the stop's name, or its map point.
// A guess then reads as one before the driver taps it.
func stopNavLabel(c Checkpoint, addr map[int]string) string {
	if a := addr[c.Ord]; a != "" {
		return a
	}
	if c.Precision == precisionFuzzed {
		return "a search for the name " + c.Name
	}
	return "the map point for " + c.Name
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
// nil, so a navigate link falls back to the stop's name; nothing of it is
// logged.
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
	return pd.forRide(a.activeSlug(), a.rideSlug).onPlan(rv.Legs).addressByOrd()
}
