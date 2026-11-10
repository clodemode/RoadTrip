package main

import "os"

type tripSwitchChoice struct {
	Slug, Name string
	Active     bool
}

// tripSwitch lists the trips the trip-keeper can show, when there is a choice:
// the rides pushed as documents to the volume. Embedded seeds and past demos
// have no file and stay out. It is built only for a signed-in driver.
func (a *App) tripSwitch() []tripSwitchChoice {
	rides, err := a.store.Rides()
	if err != nil {
		return nil
	}
	active := a.activeSlug()
	var out []tripSwitchChoice
	for _, c := range rides {
		if !validSlug(c.Slug) {
			continue
		}
		if _, err := os.Stat(tripPath(a.dataDir, c.Slug)); err != nil {
			continue
		}
		out = append(out, tripSwitchChoice{c.Slug, c.Name, c.Slug == active})
	}
	if len(out) < 2 {
		return nil
	}
	return out
}
