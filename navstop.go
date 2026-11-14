package main

import "net/http"

// stopNav is the ONE place a navigate link to a checkpoint is built: the
// driver HUD, the Ride and leg pages' stop rows and the day's end all call it.
//
// A precise stop is navigated to by its coordinates. A private (fuzzed) stop
// is a snapped cell and never navigated to by that; it goes to the stop's
// private address, as search text, when addr holds one for its ord, and to its
// name when it does not. addr comes from navAddresses and is nil for anyone
// who is not the signed-in trip-keeper, so no visitor's response is built
// from an address.
func stopNav(c Checkpoint, addr map[int]string) navLinks {
	if c.Precision == precisionFuzzed {
		return mapLinks(0, 0, stopNavText(c, addr))
	}
	return mapLinks(c.Lat, c.Lng, c.Name)
}

// stopNavText is the text a private stop is navigated to: its address when
// the trip-keeper has one on file, else its name.
func stopNavText(c Checkpoint, addr map[int]string) string {
	if c.Precision == precisionFuzzed {
		if a := addr[c.Ord]; a != "" {
			return a
		}
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
