package main

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// --- the checklist carriage -------------------------------------------------------

// checklistPush is how a checklist reaches the site: one whole document,
// parsed completely before anything is written, then written atomically to
// $DATA_DIR/trips/<slug>.checklist.json and applied to the database in the
// same request. It follows tripPush in every respect that matters: behind
// requireAuth (the session or the push token), nothing written on a refusal,
// and a log line that carries counts and the ride slug, never the text.
//
// The ride is ?ride=<slug>, else the document's own "ride", else the ride the
// site is showing. It has to be in the database already: a checklist belongs
// to a trip, so the trip is pushed first.
//
// A re-push never reaches a done step or an answered question
// (Store.ApplyChecklist).
func (a *App) checklistPush(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxChecklistBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "could not read the body (limit "+strconv.Itoa(maxChecklistBytes>>10)+" KB)", http.StatusBadRequest)
		return
	}
	doc, err := parseChecklist(raw)
	if err != nil {
		// The reason names a field by its position. It never quotes the
		// document, and neither does the log.
		log.Printf("checklist push: refused, the document did not pass the checks")
		http.Error(w, "not a checklist document: "+err.Error(), http.StatusBadRequest)
		return
	}
	slug := strings.TrimSpace(r.URL.Query().Get("ride"))
	switch {
	case slug != "" && doc.Ride != "" && slug != doc.Ride:
		http.Error(w, "?ride= and the document's ride name different rides", http.StatusBadRequest)
		return
	case slug == "" && doc.Ride != "":
		slug = doc.Ride
	case slug == "":
		slug = a.activeSlug()
	}
	if !validSlug(slug) {
		http.Error(w, "ride is not a ride slug", http.StatusBadRequest)
		return
	}
	if _, err := a.store.Ride(slug); err != nil {
		http.Error(w, "no ride "+strconv.Quote(slug)+" in the database; push its trip first", http.StatusNotFound)
		return
	}
	commit, discard, err := stageChecklist(a.dataDir, slug, raw)
	if err != nil {
		log.Printf("checklist push: staging failed: %v", err)
		http.Error(w, "could not write the checklist file", http.StatusInternalServerError)
		return
	}
	n, err := a.store.ApplyChecklist(slug, doc)
	if err != nil {
		discard()
		log.Printf("checklist push: apply failed: %v", err)
		http.Error(w, "could not apply the checklist", http.StatusInternalServerError)
		return
	}
	if err := commit(); err != nil {
		discard()
		log.Printf("checklist push: applied, but the file could not be placed: %v", err)
		http.Error(w, "the checklist was applied but its file could not be written; push again",
			http.StatusInternalServerError)
		return
	}
	log.Printf("checklist push: %s: %d stage(s), %d step(s), %d question(s); %d added, %d refreshed, %d done kept, %d dropped",
		slug, n.Stages, n.Steps, n.Questions, n.Added, n.Refreshed, n.KeptDone, n.Dropped)
	sum, _ := a.store.ChecklistSummary(slug)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "ride": slug, "checklist": n,
		"summary": sum, "active": a.activeSlug() == slug})
}

// --- the page ------------------------------------------------------------------------

// rideZone is the zone the ride is in right now: where the last arrival was,
// else where the day's leg starts. A question has no stage, so its answer is
// stamped in this zone.
func (a *App) rideZone(slug string) string {
	ride, err := a.store.Ride(slug)
	if err != nil {
		return "UTC"
	}
	legs, err := a.store.Legs(slug)
	if err != nil {
		return "UTC"
	}
	return defaultTZ(legs, defaultLeg(legs, dayOf(ride, legs, time.Now())))
}

// checklistPage is the public page: the stages in order with their progress,
// each step with its stamp and what happened, then the questions and the
// sources. The same handler serves the signed-in trip-keeper, and the only
// difference is the one argument to Store.Checklist: private text is read
// when, and only when, the request is signed in.
func (a *App) checklistPage(w http.ResponseWriter, r *http.Request) {
	slug := a.activeSlug()
	cl, err := a.store.Checklist(slug, a.auth.IsAuthed(r))
	if err != nil {
		log.Printf("ERROR checklist: %v", err)
		http.Error(w, "the checklist could not be read", http.StatusInternalServerError)
		return
	}
	a.render(w, r, "checklist.html", map[string]any{
		"Title": "Checklist", "CL": cl, "QuestionTZ": a.rideZone(slug),
		// A code in, a fixed sentence out; an unknown code is the empty string.
		"Error": checklistRefusals[r.URL.Query().Get("error")],
	})
}

// What /checklist may say about a refused check-off. The page is public and
// the redirect after a browser's form carries a CODE, never words, for the
// reason /plan's does (see planRefusals): free text in ?error= is a sentence
// of a stranger's choosing on this site. A code that is not here prints
// nothing. curl still gets the full reason in its JSON body.
const (
	clErrForm     = "form"
	clErrRide     = "ride"
	clErrMissing  = "missing"
	clErrNoteLong = "note-long"
	clErrVIN      = "vin"
	clErrAtClear  = "at-clear"
	clErrTZ       = "tz"
	clErrTime     = "time"
	clErrFuture   = "future"
	clErrFailed   = "failed"
)

var checklistRefusals = map[string]string{
	clErrForm:     "the form could not be read",
	clErrRide:     "that is not a ride",
	clErrMissing:  "this checklist has no such step or question",
	clErrNoteLong: "a note is longer than " + strconv.Itoa(maxChecklistNote) + " bytes",
	clErrVIN: "the public note holds a 17-character token shaped like a vehicle identification number; " +
		"that belongs in the private note",
	clErrAtClear: "a time and Undo contradict each other: one stamps a time, the other removes it",
	clErrTZ:      "the zone is not an IANA zone",
	clErrTime:    "the time must be a local date and time",
	clErrFuture:  "the time is in the future",
	clErrFailed:  "the change could not be saved",
}

// checklistJSON is the checklist as open data: exactly what the public page
// says, and never private text, whoever asks. The signed-in copy is the
// census below.
func (a *App) checklistJSON(w http.ResponseWriter, r *http.Request) {
	cl, err := a.store.Checklist(a.activeSlug(), false)
	if err != nil {
		http.Error(w, "the checklist could not be read", http.StatusInternalServerError)
		return
	}
	if cl == nil {
		http.Error(w, "this trip has no checklist", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, cl)
}

// checklistCensus is the whole checklist with its private text, for the
// signed-in trip-keeper and the push token. ?ride= reads a ride other than
// the one the site is showing.
func (a *App) checklistCensus(w http.ResponseWriter, r *http.Request) {
	slug := a.activeSlug()
	if v := strings.TrimSpace(r.URL.Query().Get("ride")); v != "" {
		if !validSlug(v) {
			http.Error(w, "ride is not a ride slug", http.StatusBadRequest)
			return
		}
		slug = v
	}
	cl, err := a.store.Checklist(slug, true)
	if err != nil {
		http.Error(w, "the checklist could not be read", http.StatusInternalServerError)
		return
	}
	if cl == nil {
		http.Error(w, "ride "+strconv.Quote(slug)+" has no checklist", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, cl)
}

// --- checking off ----------------------------------------------------------------------

// parseStamp reads a time the trip-keeper gave: RFC3339 with its own offset
// (curl), or the wall clock a datetime-local input posts, read in loc.
func parseStamp(v string, loc *time.Location) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	for _, layout := range []string{fmtInput, "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("the time must be a local date and time like 2030-05-01T14:30, " +
		"or RFC3339 with an offset")
}

// checklistMark checks a step off, answers a question, corrects either, or
// undoes it. POST /admin/checklist/{kind}/{id}, where kind is step or
// question and id is the document's own id for it.
//
//	(no fields)       done, now
//	at=<time>         done at that time: after the fact is the normal case
//	outcome=<text>    the PUBLIC note: what actually happened, or the answer
//	private_note=…    the private note, for the trip-keeper only
//	clear=1           undo: the stamp goes, the notes stay
//	ride=<slug>       a ride other than the one the site is showing
//
// A time is stored as UTC plus a zone: the stage's zone for a step, the zone
// the ride is in for a question (tz=<IANA> overrides that for a question).
// Posting again to a done item keeps its stamp unless at= gives a new one, so
// a note can be corrected without moving the time.
//
// The public note is held to the same rule as the document: a VIN-shaped
// token is refused, and the refusal names the field, never the value.
func (a *App) checklistMark(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("kind"), r.PathValue("id")
	if (kind != clStep && kind != clQuestion) || !validItemID(id) {
		http.NotFound(w, r)
		return
	}
	html := wantsHTML(r)
	refuse := func(status int, code, msg string) {
		if html {
			http.Redirect(w, r, "/checklist?error="+url.QueryEscape(code)+"#"+kind+"-"+id, http.StatusSeeOther)
			return
		}
		writeJSON(w, status, map[string]any{"status": "refused", "error": msg})
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		refuse(http.StatusBadRequest, clErrForm, "could not read the form")
		return
	}
	slug := a.activeSlug()
	if v := strings.TrimSpace(r.PostFormValue("ride")); v != "" {
		if !validSlug(v) {
			refuse(http.StatusBadRequest, clErrRide, "ride is not a ride slug")
			return
		}
		slug = v
	}
	row, err := a.store.checklistRow(slug, kind, id)
	if err != nil {
		refuse(http.StatusNotFound, clErrMissing, "this checklist has no "+kind+" with that id")
		return
	}

	// The notes: a field that was not sent leaves what is stored alone, so a
	// bare curl "done" does not wipe a note written from the page.
	publicName, outcome, private := "What happened", row.Outcome, row.PrivateNote
	if kind == clQuestion {
		publicName = "Answer"
	}
	if _, sent := r.PostForm["outcome"]; sent {
		outcome = strings.TrimSpace(r.PostFormValue("outcome"))
	}
	if _, sent := r.PostForm["private_note"]; sent {
		private = strings.TrimSpace(r.PostFormValue("private_note"))
	}
	if len(outcome) > maxChecklistNote || len(private) > maxChecklistNote {
		refuse(http.StatusBadRequest, clErrNoteLong, "a note is longer than "+strconv.Itoa(maxChecklistNote)+" bytes")
		return
	}
	if hasVINShape(outcome) {
		// The field, never the value: this message goes into a redirect, a
		// response body and nowhere else, and the log line below says less.
		log.Printf("checklist: a public note was refused (VIN-shaped token)")
		refuse(http.StatusBadRequest, clErrVIN, vinRefusal(publicName).Error())
		return
	}

	clearing := r.PostFormValue("clear") != ""
	at := strings.TrimSpace(r.PostFormValue("at"))
	doneAt, doneTZ := row.DoneAt, row.DoneTZ
	switch {
	case clearing && at != "":
		refuse(http.StatusBadRequest, clErrAtClear, "at and clear contradict each other: one stamps a time, the other removes it")
		return
	case clearing:
		doneAt, doneTZ = "", ""
	default:
		tz := row.StageTZ
		if kind == clQuestion {
			tz = row.DoneTZ
			if tz == "" {
				tz = a.rideZone(slug)
			}
			if v := strings.TrimSpace(r.PostFormValue("tz")); v != "" {
				if _, err := time.LoadLocation(v); err != nil || v == "Local" {
					refuse(http.StatusBadRequest, clErrTZ, "tz is not an IANA zone")
					return
				}
				tz = v
			}
		}
		if tz == "" {
			tz = "UTC"
		}
		loc := loadLoc(tz)
		switch {
		case at != "":
			t, err := parseStamp(at, loc)
			if err != nil {
				refuse(http.StatusBadRequest, clErrTime, err.Error())
				return
			}
			if t.IsZero() {
				refuse(http.StatusBadRequest, clErrTime, "the time is the zero time")
				return
			}
			// The same tolerance an arrival stamp has: clock skew, nothing more.
			if t.After(time.Now().Add(2 * time.Minute)) {
				refuse(http.StatusBadRequest, clErrFuture, "the time is in the future")
				return
			}
			// The page's own form posts the stored stamp back to the minute. That
			// is not a new time, and must not shave the seconds off the old one.
			if old, ok := parseUTC(row.DoneAt); !ok || !old.Truncate(time.Minute).Equal(t.Truncate(time.Minute)) {
				doneAt = utcString(t)
			}
			doneTZ = tz
		case row.DoneAt == "":
			doneAt, doneTZ = utcString(time.Now()), tz
		}
	}

	if err := a.store.SetChecklistItem(row.RowID, doneAt, doneTZ, outcome, private); err != nil {
		log.Printf("ERROR checklist mark: %v", err)
		refuse(http.StatusInternalServerError, clErrFailed, "could not save")
		return
	}
	sum, _ := a.store.ChecklistSummary(slug)
	// What changed and the running count. Never a note.
	switch {
	case clearing:
		log.Printf("checklist: a %s was reopened (%d of %d steps done, %d of %d questions answered)",
			kind, sum.Done, sum.Total, sum.Answered, sum.Questions)
	default:
		log.Printf("checklist: a %s was saved as done (%d of %d steps done, %d of %d questions answered)",
			kind, sum.Done, sum.Total, sum.Answered, sum.Questions)
	}
	if html {
		http.Redirect(w, r, "/checklist#"+kind+"-"+id, http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "ride": slug, "kind": kind, "id": id,
		"done": doneAt != "", "done_at": doneAt, "tz": doneTZ, "summary": sum})
}
