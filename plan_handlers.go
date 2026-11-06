package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// --- which ride the site shows -----------------------------------------------

// activeSlug resolves the ride per request: the meta key the trip-keeper set,
// when it names a ride that is really in the database, else RIDE_SLUG. Per
// request and not at boot, because under suspend this process can run for
// months and the switch to a new trip has to land without a deploy.
func (a *App) activeSlug() string {
	if v, ok := a.store.Meta(metaActiveRide); ok && v != "" {
		if _, err := a.store.Ride(v); err == nil {
			return v
		}
	}
	return a.rideSlug
}

// wantsHTML tells a form submitted by a page from a machine's POST. A browser
// form is answered with a redirect back to the page, which says what happened
// in place; curl is answered with a status and a JSON body.
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Printf("ERROR json encode: %v", err)
	}
}

// --- the planner ----------------------------------------------------------------

// planState is everything one planner request works from.
type planState struct {
	Ride    *Ride
	Doc     *planDoc
	HasFile bool       // the ride has a trip file, so there is something to choose and to save
	Saved   *Selection // the stored selection, nil when none was ever saved
	Raw     []byte     // the trip document, when HasFile
}

// loadPlan reads the active ride's plan: from its trip file when it has one,
// else from the store.
func (a *App) loadPlan() (*planState, error) {
	slug := a.activeSlug()
	ride, err := a.store.Ride(slug)
	if err != nil {
		return nil, err
	}
	st := &planState{Ride: ride}
	raw, err := readTrip(a.dataDir, slug)
	switch {
	case err == nil:
		doc, err := publishPlan(raw)
		if err != nil {
			return nil, fmt.Errorf("trip file: %w", err)
		}
		// The page names the ride by what the database holds, like every other
		// page; the file's own name line is not a second source for it.
		doc.Ride, doc.Name = ride.Slug, ride.Name
		st.Doc, st.HasFile, st.Raw = doc, true, raw
		st.Saved = a.store.StoredSelection(slug)
	case errors.Is(err, os.ErrNotExist):
		legs, err := a.store.Legs(slug)
		if err != nil {
			return nil, err
		}
		st.Doc = planFromStore(ride, legs)
	default:
		return nil, err
	}
	return st, nil
}

// savedChoices and savedStart are the plan the site is actually on: the
// stored selection over the document's defaults.
func (st *planState) savedChoices() map[string]string {
	if st.Saved == nil {
		return st.Doc.choices()
	}
	return st.Doc.choices(st.Saved.Choices)
}

func (st *planState) savedStart() string {
	if st.Saved != nil && st.Saved.StartDate != "" {
		return st.Saved.StartDate
	}
	return st.Doc.StartDate
}

const optParam = "opt-"

// requested reads a selection off a query string or a form: opt-<option> for
// each group, start for the day the ride begins. strict makes an unknown
// option, an unknown choice or an unreadable date an error; a page view is
// lenient and simply ignores what it does not recognise.
func (st *planState) requested(v url.Values, strict bool) (map[string]string, string, error) {
	over := map[string]string{}
	for key, vals := range v {
		if !strings.HasPrefix(key, optParam) || len(vals) == 0 {
			continue
		}
		opt, choice := strings.TrimPrefix(key, optParam), vals[0]
		if !st.Doc.hasChoice(opt, choice) {
			if strict {
				return nil, "", fmt.Errorf("option %q has no choice %q", opt, choice)
			}
			continue
		}
		over[opt] = choice
	}
	start := st.savedStart()
	if s := strings.TrimSpace(v.Get("start")); s != "" && st.HasFile {
		if _, err := time.Parse(dateLayout, s); err != nil {
			if strict {
				return nil, "", fmt.Errorf("start %q is not a date (YYYY-MM-DD)", s)
			}
		} else {
			start = s
		}
	}
	return st.Doc.choices(st.savedChoices(), over), start, nil
}

// planOptView is one option group as the page's form draws it.
type planOptView struct {
	ID, Title string
	Choices   []planChoiceView
}

type planChoiceView struct {
	ID, Label, Detail string
	Checked           bool
	// Note is the choice's own road, in numbers: "614.2 mi" or, when the
	// choice spans more than one day, "392.9 + 113.6 mi".
	Note string
}

func (st *planState) optViews(choices map[string]string) []planOptView {
	var out []planOptView
	for _, o := range st.Doc.Options {
		ov := planOptView{ID: o.ID, Title: o.Title}
		for _, c := range o.Choices {
			var parts []string
			for _, l := range st.Doc.Legs {
				if l.Option == o.ID && l.Choice == c.ID {
					parts = append(parts, fmtMiles(l.Miles))
				}
			}
			note := ""
			if len(parts) > 0 {
				note = strings.Join(parts, " + ") + " mi"
			}
			ov.Choices = append(ov.Choices, planChoiceView{ID: c.ID, Label: c.Label, Detail: c.Detail,
				Checked: choices[o.ID] == c.ID, Note: note})
		}
		out = append(out, ov)
	}
	return out
}

// fmtMiles writes a mileage to one decimal, always: 400.4, 600.0.
func fmtMiles(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

func (a *App) planPage(w http.ResponseWriter, r *http.Request) {
	st, err := a.loadPlan()
	if err != nil {
		log.Printf("ERROR plan: %v", err)
		http.Error(w, "the plan could not be read", http.StatusInternalServerError)
		return
	}
	choices, start, _ := st.requested(r.URL.Query(), false)
	sum := st.Doc.summarize(choices, start)
	page := map[string]any{
		"doc": st.Doc, "choices": choices, "start": start,
		"saved": map[string]any{"choices": st.savedChoices(), "start": st.savedStart()},
	}
	pageLeg := ""
	if legs, err := a.store.Legs(st.Ride.Slug); err == nil && len(legs) > 0 {
		pageLeg = legs[0].Slug
	}
	a.render(w, r, "plan.html", map[string]any{
		"Title": "Plan", "Ride": st.Ride, "Sum": sum, "Opts": st.optViews(choices),
		"Start": start, "HasFile": st.HasFile, "Plan": page, "PageLeg": pageLeg,
		"Error": r.URL.Query().Get("error"), "Saved": r.URL.Query().Get("saved") != "",
	})
}

// planJSON is the planner's arithmetic as data: the days and totals for the
// saved plan, or for whatever selection the query asks about. It answers the
// question "what would that choice cost" without changing anything.
func (a *App) planJSON(w http.ResponseWriter, r *http.Request) {
	st, err := a.loadPlan()
	if err != nil {
		log.Printf("ERROR plan: %v", err)
		http.Error(w, "the plan could not be read", http.StatusInternalServerError)
		return
	}
	choices, start, err := st.requested(r.URL.Query(), true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plan":    st.Doc.summarize(choices, start),
		"options": st.Doc.Options,
		"saved":   map[string]any{"selection": st.savedChoices(), "start_date": st.savedStart()},
	})
}

// planSave stores a selection and re-plans the ride under it, at once. It is
// the only way a choice or the start date changes for everyone, and it is
// refused whole when the new plan would reach an arrived checkpoint or a leg
// or checkpoint that holds posts (Store.planConflicts): the road already
// driven is not the plan's to move.
func (a *App) planSave(w http.ResponseWriter, r *http.Request) {
	html := wantsHTML(r)
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not read the form", http.StatusBadRequest)
		return
	}
	refuse := func(status int, msg string) {
		if html {
			q := url.Values{"error": {msg}}
			for k, v := range r.PostForm {
				if (strings.HasPrefix(k, optParam) || k == "start") && len(v) > 0 {
					q.Set(k, v[0])
				}
			}
			http.Redirect(w, r, "/plan?"+q.Encode(), http.StatusSeeOther)
			return
		}
		writeJSON(w, status, map[string]any{"status": "refused", "error": msg})
	}
	st, err := a.loadPlan()
	if err != nil {
		log.Printf("ERROR plan: %v", err)
		refuse(http.StatusInternalServerError, "the plan could not be read")
		return
	}
	if !st.HasFile {
		refuse(http.StatusConflict, "this ride has no trip file, so it has nothing to choose")
		return
	}
	choices, start, err := st.requested(r.PostForm, true)
	if err != nil {
		refuse(http.StatusBadRequest, err.Error())
		return
	}
	sel := &Selection{StartDate: start, Choices: choices}
	sd, err := parseSeedBytes(st.Raw, st.Ride.Slug, sel)
	if err == nil {
		err = checkPlan(sd)
	}
	if err != nil {
		refuse(http.StatusBadRequest, err.Error())
		return
	}
	_, n, err := a.store.ApplyPlan(sd)
	var conflict *PlanConflictError
	switch {
	case errors.As(err, &conflict):
		log.Printf("plan: selection refused, %d conflict(s)", len(conflict.Reasons))
		refuse(http.StatusConflict, conflict.Error())
		return
	case err != nil:
		log.Printf("ERROR plan save: %v", err)
		refuse(http.StatusInternalServerError, "could not save the plan")
		return
	}
	// Counts, never a place or a date.
	log.Printf("plan: selection saved for the active ride: %s", n)
	if html {
		http.Redirect(w, r, "/plan?saved=1", http.StatusSeeOther)
		return
	}
	doc, err := publishPlan(st.Raw)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "selection": sd.Selection})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "selection": sd.Selection,
		"plan": doc.summarize(sd.Selection.Choices, sd.Selection.StartDate)})
}

// --- trips on the volume ----------------------------------------------------------

// tripPush is how a trip reaches the site: one whole trip document, parsed
// completely before anything is written, then written atomically to
// $DATA_DIR/trips/<slug>.geojson and applied to the database in the same
// request — a suspended machine never re-runs boot, so "it loads next boot"
// would mean "it loads next deploy".
//
// It is behind requireAuth like /admin/private: the session or the push
// token. ?activate=1 also makes the pushed trip the ride the site shows.
//
// A re-push of a trip already under way is held to the same guard a saved
// selection is: it is refused whole if it would reach an arrived checkpoint
// or anything that holds posts.
func (a *App) tripPush(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTripBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "could not read the body (limit "+strconv.Itoa(maxTripBytes>>20)+" MB)", http.StatusBadRequest)
		return
	}
	slug, err := checkTrip(raw)
	if err != nil {
		http.Error(w, "not a trip document: "+err.Error(), http.StatusBadRequest)
		return
	}
	if embeddedRideSlugs()[slug] {
		http.Error(w, "ride "+strconv.Quote(slug)+" ships with the site and cannot be replaced by a push",
			http.StatusConflict)
		return
	}
	commit, discard, err := stageTrip(a.dataDir, slug, raw)
	if err != nil {
		log.Printf("trip push: staging failed: %v", err)
		http.Error(w, "could not write the trip file", http.StatusInternalServerError)
		return
	}
	created, n, sd, err := applyTrip(a.store, raw, slug)
	var conflict *PlanConflictError
	switch {
	case errors.As(err, &conflict):
		discard()
		log.Printf("trip push: refused, %d conflict(s)", len(conflict.Reasons))
		writeJSON(w, http.StatusConflict, map[string]any{"status": "refused", "error": conflict.Error()})
		return
	case err != nil:
		discard()
		log.Printf("trip push: apply failed: %v", err)
		http.Error(w, "could not apply the trip: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := commit(); err != nil {
		// The database already holds the new plan; the old file (or none) is
		// still on the volume and the next boot would re-apply it. Say so —
		// the pusher is watching, and pushing again is the whole repair.
		discard()
		log.Printf("trip push: the plan was applied but the file could not be placed: %v", err)
		http.Error(w, "the plan was applied but the trip file could not be written; push again",
			http.StatusInternalServerError)
		return
	}
	activated := false
	if r.URL.Query().Get("activate") != "" {
		if err := a.store.SetMeta(metaActiveRide, slug); err != nil {
			log.Printf("trip push: could not activate: %v", err)
			http.Error(w, "the trip was stored but could not be made active", http.StatusInternalServerError)
			return
		}
		activated = true
	}
	// The slug and counts, never a place or a date.
	if created {
		log.Printf("trip push: %s created (%d legs, %d checkpoints)", slug, len(sd.Legs), len(sd.Checkpoints))
	} else {
		log.Printf("trip push: %s refreshed: %s", slug, n)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "ride": slug, "created": created,
		"legs": len(sd.Legs), "checkpoints": len(sd.Checkpoints), "options": len(sd.Options),
		"selection": sd.Selection, "active": a.activeSlug() == slug, "activated": activated,
	})
}

// rideActivate sets which ride the site shows: slug=<ride> for a ride that is
// in the database, or clear=1 to fall back to RIDE_SLUG. Nothing about the
// ride that stops being shown is touched; it stays in the database whole.
func (a *App) rideActivate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if r.PostFormValue("clear") != "" {
		if err := a.store.DeleteMeta(metaActiveRide); err != nil {
			http.Error(w, "could not clear the active ride", http.StatusInternalServerError)
			return
		}
		log.Printf("active ride: cleared, the site shows RIDE_SLUG")
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "active": a.activeSlug(), "source": "RIDE_SLUG"})
		return
	}
	slug := strings.TrimSpace(r.PostFormValue("slug"))
	if !validSlug(slug) {
		http.Error(w, "slug is not a ride slug", http.StatusBadRequest)
		return
	}
	if _, err := a.store.Ride(slug); err != nil {
		http.Error(w, "no ride "+strconv.Quote(slug)+" in the database", http.StatusNotFound)
		return
	}
	if err := a.store.SetMeta(metaActiveRide, slug); err != nil {
		http.Error(w, "could not set the active ride", http.StatusInternalServerError)
		return
	}
	log.Printf("active ride: %s", slug)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "active": slug, "source": "meta"})
}

// ridesJSON is the census of rides: every ride in the database with its row
// counts, and which one the site is showing. It is how "the earlier ride is
// still there" is checked after the active ride moves.
func (a *App) ridesJSON(w http.ResponseWriter, r *http.Request) {
	rides, err := a.store.Rides()
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	type row struct {
		RideCensus
		Active   bool `json:"active"`
		TripFile bool `json:"trip_file"`
	}
	active := a.activeSlug()
	out := []row{}
	for _, c := range rides {
		hasFile := false
		if validSlug(c.Slug) {
			_, err := os.Stat(tripPath(a.dataDir, c.Slug))
			hasFile = err == nil
		}
		out = append(out, row{RideCensus: c, Active: c.Slug == active, TripFile: hasFile})
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": active, "fallback": a.rideSlug, "rides": out})
}
