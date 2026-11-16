package main

// The Listen Inn's own addresses (Kris, 2026-11-16). The Listen Inn is a card
// docked on the Ride page; these two routes serve that same page with the card
// already open:
//
//	GET /listeninn            the card open on the newest song
//	GET /listeninn/song/{id}  the card open on one song, its Play button armed
//
// {id} is the opaque public name of a plays row, the same name
// /tracks/{id}/audio takes: it is made once when the play is stored and never
// changes, and it says nothing about the album or the library. Nothing here
// starts sound: a browser will not, and the page does not ask it to.
//
// The routes are exactly as open as the Listen Inn on the Ride page: the
// whole-site gate (SITE_VISIBILITY) is in front of them like every page, only
// a song in the active ride's list can be named, and with PUBLIC_LISTEN off
// the song is shown with nothing to play. A name that is not in the list is a
// 404 that still shows the Ride page and says so in the card.

import (
	"fmt"
	"net/http"
)

// listenAsk is what a Listen Inn address asks of the Ride page.
type listenAsk struct {
	song string // "" for /listeninn
}

func (a *App) listenInn(w http.ResponseWriter, r *http.Request) {
	a.ridePage(w, r, &listenAsk{})
}

func (a *App) listenInnSong(w http.ResponseWriter, r *http.Request) {
	a.ridePage(w, r, &listenAsk{song: r.PathValue("id")})
}

// listenCard fills what the card reads. ask is nil on the plain Ride page.
// It answers false when it has already written the response.
func (a *App) listenCard(w http.ResponseWriter, r *http.Request, data map[string]any, doc *tracksDoc, rideName string, ask *listenAsk) bool {
	if doc == nil || len(doc.Tracks) == 0 {
		if ask == nil {
			return true
		}
		if ask.song == "" {
			// Nothing has played yet: the Ride page is the whole answer.
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return false
		}
		listenText(w, http.StatusNotFound, "That song is not on this trip's list.")
		return false
	}
	// The Play button is armed on the newest song unless one was asked for.
	data["ListenArm"] = doc.Tracks[0].ID
	if ask == nil {
		return true
	}
	data["ListenOpen"] = true
	if ask.song == "" {
		data["Title"] = "Listen Inn"
		data["Description"] = fmt.Sprintf("Listen Inn: the songs played on %s.", rideName)
		return true
	}
	for i := range doc.Tracks {
		if v := doc.Tracks[i]; v.ID != "" && v.ID == ask.song {
			data["ListenSong"] = &v
			data["ListenArm"] = v.ID
			data["Title"] = fmt.Sprintf("%s by %s · Listen Inn", v.Track, v.Artist)
			from := ""
			if v.Album != "" {
				from = " from " + v.Album
			}
			data["Description"] = fmt.Sprintf("%s by %s%s, played on %s.", v.Track, v.Artist, from, rideName)
			return true
		}
	}
	data["ListenMissing"] = true
	data["Title"] = "Listen Inn"
	data["Status"] = http.StatusNotFound // render writes it after its own headers
	return true
}
