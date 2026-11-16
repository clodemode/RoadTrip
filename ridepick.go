package main

import (
	"net/http"
	"net/url"
	"os"
	"sort"
)

// The ride picker (Kris, 2026-11-16): "Our Ride/Drive pages need to let us
// select previous or next roadtrips, or current. Guests only see current and
// past as select options."
//
// Two controls, one list:
//
//   - On the Ride page (and a leg page) everyone gets a picker that LOOKS at a
//     trip without changing what the site shows: GET ?ride=<slug>. A visitor is
//     offered the current trip and the past ones. A signed-in user who may
//     ride a next trip is offered it too.
//   - On the Driver page and the Plan page the trip-keeper (an admin) gets the
//     switch that CHANGES the trip the site shows: POST /driver/ride/active.
//
// "Past", "current" and "next" are read against the trip the site shows, by
// start date: earlier is past, later (or the same day) is next. The trip the
// site shows is itself current only until it is finished; a finished one is
// past, and then nothing is current. A next trip is
// where someone will be and when, which a visitor is never told, so a visitor
// who asks for one by slug is shown the current trip as if nothing was asked.
//
// The list is the trips pushed to the volume (a trip file on disk), the same
// list the switch has always offered: embedded seeds and old demos stay out.
// The current trip is always in it.

const (
	whenPast    = "past"
	whenCurrent = "current"
	whenNext    = "next"
)

type rideChoice struct {
	Slug, Name, StartDate string
	When                  string // past | current | next
	Current               bool   // the trip the site shows
	Selected              bool   // the trip this page is drawn from
}

// rideGroup is one <optgroup>: Past, Current or Next, in that order.
type rideGroup struct {
	Label   string
	Choices []rideChoice
}

// allRideChoices lists every trip on the volume plus the current one, each
// placed past/current/next against the current, oldest first.
func (a *App) allRideChoices() []rideChoice {
	rides, err := a.store.Rides()
	if err != nil {
		return nil
	}
	active := a.activeSlug()
	var cur *RideCensus
	for i := range rides {
		if rides[i].Slug == active {
			cur = &rides[i]
		}
	}
	// The trip the site shows is current only while it is unfinished: once
	// it is driven it is past, and until another starts there is no current
	// trip (Kris, 2026-11-16, of Kingston after it ended).
	activeDone := a.rideIsFinished()
	var out []rideChoice
	for _, c := range rides {
		isCur := c.Slug == active
		if !isCur {
			if !validSlug(c.Slug) {
				continue
			}
			if _, err := os.Stat(tripPath(a.dataDir, c.Slug)); err != nil {
				continue
			}
		}
		when := whenNext
		switch {
		case isCur && activeDone:
			when = whenPast
		case isCur:
			when = whenCurrent
		case cur != nil && c.StartDate != "" && c.StartDate < cur.StartDate:
			when = whenPast
		}
		out = append(out, rideChoice{Slug: c.Slug, Name: c.Name, StartDate: c.StartDate, When: when, Current: isCur})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartDate != out[j].StartDate {
			return out[i].StartDate < out[j].StartDate
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

// mayLookAt says whether this request may be shown choice c on the Ride page:
// the current trip and past trips to anyone, a next trip only to a signed-in
// user who may ride it (an admin may ride every trip).
func (a *App) mayLookAt(r *http.Request, c rideChoice) bool {
	if c.When != whenNext {
		return true
	}
	return a.auth.IsAuthed(r) && a.auth.mayRide(r, c.Slug, a.activeSlug())
}

// viewedSlug is the trip a Ride or leg page is drawn from: ?ride=<slug> when
// this request may look at that trip, else the trip the site shows. A slug it
// may not look at is treated exactly like a slug that does not exist, so the
// answer never says that a next trip is there.
func (a *App) viewedSlug(r *http.Request) string {
	active := a.activeSlug()
	want := r.URL.Query().Get("ride")
	if want == "" || want == active || !validSlug(want) {
		return active
	}
	for _, c := range a.allRideChoices() {
		if c.Slug == want && a.mayLookAt(r, c) {
			return want
		}
	}
	return active
}

// ridePick is the look-at picker for this request, grouped. nil when there is
// nothing to choose between, so the page draws no control.
func (a *App) ridePick(r *http.Request, viewed string) []rideGroup {
	var shown []rideChoice
	for _, c := range a.allRideChoices() {
		if a.mayLookAt(r, c) {
			c.Selected = c.Slug == viewed
			shown = append(shown, c)
		}
	}
	return groupRides(shown)
}

// tripSwitch is the trip-keeper's switch, grouped: every trip on the volume,
// the one the site shows marked. nil when there is no choice to make.
func (a *App) tripSwitch() []rideGroup {
	all := a.allRideChoices()
	for i := range all {
		all[i].Selected = all[i].Current
	}
	return groupRides(all)
}

func groupRides(cs []rideChoice) []rideGroup {
	if len(cs) < 2 {
		return nil
	}
	var past, cur, next []rideChoice
	for _, c := range cs {
		switch c.When {
		case whenPast:
			past = append(past, c)
		case whenCurrent:
			cur = append(cur, c)
		default:
			next = append(next, c)
		}
	}
	// The nearest past trip first: the list reads outward from now.
	for i, j := 0, len(past)-1; i < j; i, j = i+1, j-1 {
		past[i], past[j] = past[j], past[i]
	}
	var out []rideGroup
	for _, g := range []rideGroup{{"Current", cur}, {"Next", next}, {"Past", past}} {
		if len(g.Choices) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// rideQuery is the query a link on a looked-at trip's page carries so the
// next page stays on that trip: "" on the current trip.
func rideQuery(viewed, active string) string {
	if viewed == active {
		return ""
	}
	return "?ride=" + url.QueryEscape(viewed)
}

// lookingAt fills what the Ride and leg pages need for the picker: the
// grouped list (RidePick), the query their links carry (RideQ), and, only
// when the page is drawn from another trip than the one the site shows,
// LookingAt: that trip's choice, which turns the page into a record.
func (a *App) lookingAt(r *http.Request, data map[string]any, viewed string) {
	active := a.activeSlug()
	data["RidePick"] = a.ridePick(r, viewed)
	data["RideQ"] = rideQuery(viewed, active)
	if viewed == active {
		return
	}
	for _, c := range a.allRideChoices() {
		if c.Slug == viewed {
			c.Selected = true
			data["LookingAt"] = c
			return
		}
	}
}
