package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Every stop is navigable before a trip goes live (Kris, 2026-11-18: "Must
// use precise addresses for navigation."). The decision lives in navTarget
// (navstop.go); this file is what asks it at the edges: the trip push names
// the stops with no address, going live is refused while one remains, the
// overlay push refuses an entry that matches no stop, and the readiness page
// shows where every Navigate link will go.
//
// The check applies at push and at activation, never at boot, so no ride
// already on the volume is refused for what it lacked when it was made.

// noAddressNote is the sentence the Plan page and the driver screen carry
// while stops have no address, or "" when none lacks one.
func noAddressNote(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return "1 stop has no address: " + names[0] + ". Navigation is off for it."
	}
	return strconv.Itoa(len(names)) + " stops have no address: " + strings.Join(names, ", ") + ". Navigation is off for them."
}

// activationRefusal is the 409 body for a ride that cannot go live.
func activationRefusal(slug string, missing []string) map[string]any {
	return map[string]any{
		"status": "refused", "ride": slug, "needs_address": missing,
		"error": "not made active: " + noAddressNote(missing) +
			" File an address for each in the private overlay, or mark the stop \"nav\": \"point\" in the trip document.",
	}
}

// stopNamesByOrd is every name a stop number may carry on a ride: the stops of
// the plan the database holds and every stop of every alternative in the trip
// file, so an entry filed for a stop of a route not currently chosen still
// matches the stop it was filed for.
func (a *App) stopNamesByOrd(slug string) (map[int][]string, error) {
	legs, err := a.store.Legs(slug)
	if err != nil {
		return nil, err
	}
	out := map[int][]string{}
	for _, l := range legs {
		for _, c := range l.Checkpoints {
			out[c.Ord] = append(out[c.Ord], c.Name)
		}
	}
	if raw, err := readTrip(a.dataDir, slug); err == nil {
		if doc, err := publishPlan(raw); err == nil {
			for _, c := range doc.Checkpoints {
				out[c.Ord] = append(out[c.Ord], c.Name)
			}
		}
	}
	return out, nil
}

// privateMisses names every entry of one ride's block that matches no stop:
// its number on no stop, or its "for" equal to no stop name at that number.
// It names the entry number and the "for" text, never the address.
func privateMisses(cps map[string]privateCheckpoint, names map[int][]string) []string {
	keys := make([]string, 0, len(cps))
	for k := range cps {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		b, _ := strconv.Atoi(keys[j])
		return a < b
	})
	var out []string
	for _, k := range keys {
		ord, err := strconv.Atoi(k)
		if err != nil {
			continue // the key check refuses these first
		}
		have := names[ord]
		if len(have) == 0 {
			out = append(out, fmt.Sprintf("entry %d: no stop is numbered %d", ord, ord))
			continue
		}
		f := strings.TrimSpace(cps[k].For)
		if f == "" {
			continue
		}
		hit := false
		for _, n := range have {
			if strings.EqualFold(f, n) {
				hit = true
			}
		}
		if !hit {
			out = append(out, fmt.Sprintf("entry %d: filed for %q, which no stop numbered %d carries", ord, f, ord))
		}
	}
	return out
}

// refuseUnmatched answers 400 and reports true when a block holds an entry
// that matches no stop of its ride. A ride not in the database cannot be
// judged and is let through.
func (a *App) refuseUnmatched(w http.ResponseWriter, slug string, cps map[string]privateCheckpoint) bool {
	if len(cps) == 0 {
		return false
	}
	if _, err := a.store.Ride(slug); err != nil {
		return false
	}
	names, err := a.stopNamesByOrd(slug)
	if err != nil {
		http.Error(w, "database error", 500)
		return true
	}
	miss := privateMisses(cps, names)
	if len(miss) == 0 {
		return false
	}
	log.Printf("private overlay: push refused, %d entry(ies) match no stop", len(miss))
	http.Error(w, "refused, nothing was saved. Filed data that matches no stop is not kept:\n"+strings.Join(miss, "\n")+"\n", 400)
	return true
}

// privatePushRide is POST /driver/private/ride/{slug}: one ride's block of the
// overlay, {"checkpoints": {…}}, replacing that ride's block and no other. An
// entry that matches no stop refuses the whole push by name.
func (a *App) privatePushRide(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !validSlug(slug) {
		http.Error(w, "ride is not a ride slug", 400)
		return
	}
	if _, err := a.store.Ride(slug); err != nil {
		http.Error(w, "no ride "+strconv.Quote(slug)+" in the database", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPrivateBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "could not read the body (limit "+strconv.Itoa(maxPrivateBytes>>10)+" KB)", 400)
		return
	}
	var in privateData
	if err := json.Unmarshal(raw, &in); err != nil {
		http.Error(w, "not valid JSON: "+err.Error(), 400)
		return
	}
	if in.Rides != nil {
		http.Error(w, "this path takes one ride's block, {\"checkpoints\": {…}}, not a document with rides", 400)
		return
	}
	for k := range in.Checkpoints {
		if _, err := strconv.Atoi(k); err != nil {
			http.Error(w, "checkpoint key "+strconv.Quote(k)+" is not a checkpoint ord", 400)
			return
		}
	}
	if a.refuseUnmatched(w, slug, in.Checkpoints) {
		return
	}
	out, err := loadPrivate(a.dataDir)
	if err != nil {
		log.Printf("private overlay: push refused, the overlay on the volume does not parse: %v", err)
		http.Error(w, "the overlay on the volume does not parse; push a whole document (with rides) to replace it", http.StatusConflict)
		return
	}
	out.setRide(slug, a.rideSlug, in.Checkpoints)
	if err := savePrivate(a.dataDir, out); err != nil {
		log.Printf("private overlay: push failed: %v", err)
		http.Error(w, "could not write the overlay", 500)
		return
	}
	log.Printf("private overlay: pushed, %d checkpoint(s) carry detail; the overlay now holds %d", len(in.Checkpoints), out.entries())
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, "{\"status\":\"ok\",\"ride\":%q,\"checkpoints\":%d,\"total\":%d}\n", slug, len(in.Checkpoints), out.entries())
}

// --- the readiness page --------------------------------------------------------

// readyRow is one stop on /driver/ready.
type readyRow struct {
	Ord     int
	Name    string
	Start   bool
	Kind    navKind
	Goes    string // where Navigate will go, in words; empty with no target
	Links   navLinks
	Checked bool
}

func readyKey(slug string, ord int) string { return "ready:" + slug + ":" + strconv.Itoa(ord) }

// readyPrint is what a tick is a tick OF: the stop's name, point and address.
// A tick stored against another print is stale, which is how it clears when
// any of them changes. It is a hash, so the meta table never holds the address.
func readyPrint(c Checkpoint, addr string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%v|%v|%s", c.Name, c.Lat, c.Lng, addr)))
	return hex.EncodeToString(sum[:])
}

func (a *App) readyRows(r *http.Request) (*rideView, []readyRow, error) {
	rv, err := a.loadRide()
	if err != nil {
		return nil, nil, err
	}
	addr := a.navAddresses(r)
	var rows []readyRow
	for _, l := range rv.Legs {
		for _, c := range l.Checkpoints {
			row := readyRow{Ord: c.Ord, Name: c.Name, Start: c.IsStart, Kind: navTarget(c, addr)}
			if row.Kind != navNone {
				row.Goes = stopNavLabel(c, addr)
				row.Links = stopNav(c, addr)
			}
			if v, ok := a.store.Meta(readyKey(rv.Ride.Slug, c.Ord)); ok && v == readyPrint(c, addr[c.Ord]) {
				row.Checked = true
			}
			rows = append(rows, row)
		}
	}
	return rv, rows, nil
}

// readyPage is GET /driver/ready: one row per stop in order with where
// Navigate will go and the three links to open. The check is the trip-keeper
// opening the link; no geocoder is called.
func (a *App) readyPage(w http.ResponseWriter, r *http.Request) {
	rv, rows, err := a.readyRows(r)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	missing := []string{}
	for _, row := range rows {
		if row.Kind == navNone && !row.Start {
			missing = append(missing, row.Name)
		}
	}
	a.render(w, r, "ready.html", map[string]any{
		"Title": "Ready to go", "Ride": rv.Ride, "Rows": rows, "Missing": noAddressNote(missing),
	})
}

// readyTick is POST /driver/ready/stop/{ord}: the trip-keeper's "Checked"
// tick for one stop, kept on the server, against what the stop is now.
func (a *App) readyTick(w http.ResponseWriter, r *http.Request) {
	ord, err := strconv.Atoi(r.PathValue("ord"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rv, err := a.loadRide()
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	addr := a.navAddresses(r)
	for _, l := range rv.Legs {
		for _, c := range l.Checkpoints {
			if c.Ord != ord {
				continue
			}
			key := readyKey(rv.Ride.Slug, ord)
			var werr error
			if r.PostFormValue("checked") == "0" {
				werr = a.store.DeleteMeta(key)
			} else {
				werr = a.store.SetMeta(key, readyPrint(c, addr[ord]))
			}
			if werr != nil {
				http.Error(w, "could not save the tick", 500)
				return
			}
			http.Redirect(w, r, "/driver/ready", http.StatusSeeOther)
			return
		}
	}
	http.NotFound(w, r)
}

// noAddressFor is the notice for the active ride on the pages that carry it.
func (a *App) noAddressFor() string {
	miss, err := a.rideNavMissing(a.activeSlug())
	if err != nil {
		return ""
	}
	return noAddressNote(miss)
}
