package main

import "net/http"

// The maps app the stop row's Navigate opens (Kris, 2026-11-18: one tap, and
// the tap goes to the app the traveller uses). It is a choice per signed-in
// user, kept on the server in the meta table under "maps:<user>", so a phone,
// a second phone and a machine all read the same thing and no browser stores
// it. Waze is the default. It changes only which of the three links
// (stopNav) the button opens; it never changes where they go. When the
// traveller profile ships this value moves there.

type mapsApp struct {
	Key, Name string
	On        bool
}

var mapsChoices = []mapsApp{{Key: "waze", Name: "Waze"}, {Key: "google", Name: "Google Maps"}, {Key: "apple", Name: "Apple Maps"}}

func metaMapsKey(user string) string { return "maps:" + user }

// mapsChoice is the user's app key: a stored one that is still offered, else waze.
func (a *App) mapsChoice(user string) string {
	if v, ok := a.store.Meta(metaMapsKey(user)); ok {
		for _, m := range mapsChoices {
			if m.Key == v {
				return v
			}
		}
	}
	return "waze"
}

// mapsApps is the choices with the user's own marked on, for the screen.
func (a *App) mapsApps(user string) []mapsApp {
	cur := a.mapsChoice(user)
	out := make([]mapsApp, len(mapsChoices))
	for i, m := range mapsChoices {
		m.On = m.Key == cur
		out[i] = m
	}
	return out
}

// pickMaps sets the one link Navigate opens, from the three the view carries.
func (a *App) pickMaps(h *hudView, user string) {
	switch a.mapsChoice(user) {
	case "google":
		h.NavHref = h.Google
	case "apple":
		h.NavHref = h.Apple
	default:
		h.NavHref = h.Waze
	}
}

// setMapsApp is POST /driver/maps: app=waze|google|apple. Anything else is refused.
func (a *App) setMapsApp(w http.ResponseWriter, r *http.Request) {
	app := r.FormValue("app")
	for _, m := range mapsChoices {
		if m.Key == app {
			if err := a.store.SetMeta(metaMapsKey(a.userOf(r)), app); err != nil {
				http.Error(w, "the choice could not be saved", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/driver", http.StatusSeeOther)
			return
		}
	}
	http.Error(w, "choose waze, google or apple", http.StatusBadRequest)
}
