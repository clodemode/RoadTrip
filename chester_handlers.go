package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// --- Chester's pages ---------------------------------------------------------------
//
// Every route here is behind requireAuth: the session or the push token, and
// nothing else. That is the spend control as much as the privacy one, since
// an anonymous question would be somebody else spending the trip-keeper's
// money. Nothing of the log reaches an anonymous page or an open endpoint.

// chesterPresets are the one-tap questions. The words that are sent are
// fixed here, so a preset asked twice is the same question twice and the log
// can compare them.
var chesterPresets = []struct{ ID, Label, Question string }{
	{"fuel", "Fuel", "Where is a good place for fuel soon, on today's route?"},
	{"food", "Food", "Where is a good place to eat soon, on today's route?"},
	{"rest", "Rest stop", "Where is the next rest stop, or a good place to stretch, on today's route?"},
	{"coffee", "Coffee", "Where can we get good coffee soon, on today's route?"},
	{"lodging", "Tonight's lodging", "Where could we stay tonight, at or near tonight's stop? Give real options and what to check before booking."},
	{"stop", "Worth a stop", "Is there anything worth a short stop ahead on today's route?"},
	{"weather", "Weather and roads ahead", "What are the weather and the road conditions ahead on today's route? Say what you could confirm and what you could not."},
	{"howfar", "How far to tonight", "How far is it to tonight's stop, and roughly how long will it take? Say that it is an estimate."},
}

func chesterPreset(id string) (string, bool) {
	for _, p := range chesterPresets {
		if p.ID == id {
			return p.Question, true
		}
	}
	return "", false
}

// The sentences a question can be refused with before it is ever sent. Each
// goes to the page as it is, and the question stays in the box.
const (
	chesterSaidOff      = "Chester is not set up on this site."
	chesterSaidBusy     = "Chester is busy with another question. Ask again when it has finished."
	chesterSaidEmpty    = "Ask a question first."
	chesterSaidLong     = "The question is too long. Keep it under 1,000 characters."
	chesterSaidMode     = "The mode is quick or plan."
	chesterSaidPreset   = "That is not one of the preset questions."
	chesterSaidNoRide   = "The ride could not be read, so Chester has nothing to go on."
	chesterSaidNoLedger = "Today's spending could not be read, so Chester will not ask."
)

func usd2(v float64) string { return "$" + strconv.FormatFloat(v, 'f', 2, 64) }
func usd4(v float64) string { return "$" + strconv.FormatFloat(v, 'f', 4, 64) }

// chesterPage is the helper itself: the presets, the box, the switch, the
// answer and its cards. With no key it says so and offers nothing to press.
func (a *App) chesterPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Title": "Chester", "On": a.chester != nil, "Presets": chesterPresets}
	if a.chester != nil {
		cfg := a.chester.cfg
		spent, _ := a.store.ChesterSpent(time.Now())
		data["Spent"], data["Cap"] = usd2(spent), usd2(cfg.DailyUSD)
		data["Quick"], data["Plan"] = cfg.Quick, cfg.Plan
		data["QuickSecs"], data["PlanSecs"] = int(cfg.Quick.Timeout.Seconds()), int(cfg.Plan.Timeout.Seconds())
	}
	a.render(w, r, "chester.html", data)
}

// chesterAsk takes one question and streams the answer back, one JSON object
// a line: {"t":"text","d":"…"} as the words arrive, "status" while it
// thinks or searches, "places" with the cards, "error" with one plain
// sentence, and "done" last, with the row's id and what it cost.
//
//	mode=quick|plan   default quick
//	preset=<id>       one of the fixed questions, or
//	q=<text>          the trip-keeper's own
//	lat=, lng=        the phone's position, when it was sent
//
// A question that is refused before it is sent (no key, busy, over the day's
// limit, nothing asked) is answered with a status and a JSON body carrying
// one sentence, and no row is written: nothing was spent.
func (a *App) chesterAsk(w http.ResponseWriter, r *http.Request) {
	refuse := func(status int, msg string) {
		writeJSON(w, status, map[string]any{"status": "refused", "error": msg})
	}
	if a.chester == nil {
		refuse(http.StatusServiceUnavailable, chesterSaidOff)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		refuse(http.StatusBadRequest, chesterSaidLong)
		return
	}
	mode := strings.TrimSpace(r.PostFormValue("mode"))
	switch mode {
	case "":
		mode = chesterQuick
	case chesterQuick, chesterPlan:
	default:
		refuse(http.StatusBadRequest, chesterSaidMode)
		return
	}
	preset := strings.TrimSpace(r.PostFormValue("preset"))
	question := strings.TrimSpace(r.PostFormValue("q"))
	if preset != "" {
		q, ok := chesterPreset(preset)
		if !ok {
			refuse(http.StatusBadRequest, chesterSaidPreset)
			return
		}
		question = q
	}
	switch {
	case question == "":
		refuse(http.StatusBadRequest, chesterSaidEmpty)
		return
	case len(question) > maxChesterQuestion:
		refuse(http.StatusBadRequest, chesterSaidLong)
		return
	}
	// Half a coordinate is no coordinate.
	lat, lng := formCoord(r, "lat", 90), formCoord(r, "lng", 180)
	if lat == nil || lng == nil {
		lat, lng = nil, nil
	}

	// One question at a time: a second one is told so at once rather than
	// queued, because a queue on a hotspot is a page that looks hung.
	if !a.chesterBusy.TryLock() {
		refuse(http.StatusConflict, chesterSaidBusy)
		return
	}
	defer a.chesterBusy.Unlock()

	cfg := a.chester.cfg
	now := time.Now()
	spent, err := a.store.ChesterSpent(now)
	if err != nil {
		log.Printf("ERROR chester: reading the day's spend: %v", err)
		refuse(http.StatusInternalServerError, chesterSaidNoLedger)
		return
	}
	if spent >= cfg.DailyUSD {
		log.Printf("chester: refused, the day's limit is reached (%s of %s)", usd2(spent), usd2(cfg.DailyUSD))
		refuse(http.StatusTooManyRequests, fmt.Sprintf(
			"Chester has reached today's limit: %s of %s spent. It starts again at midnight UTC.",
			usd2(spent), usd2(cfg.DailyUSD)))
		return
	}
	cx, err := a.chesterContext(now, lat, lng)
	if err != nil {
		log.Printf("ERROR chester: reading the ride: %v", err)
		refuse(http.StatusInternalServerError, chesterSaidNoRide)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flush := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	emit := func(ev chesterEvent) {
		if enc.Encode(ev) == nil {
			_ = flush.Flush()
		}
	}

	m := cfg.mode(mode)
	res := a.chester.ask(r.Context(), mode, cx, question, emit)
	usd := chesterCost(m.Model, res.Served, res.Usage)
	note := strings.Join(res.Notes, "; ")
	if res.ErrKind != "" {
		if note != "" {
			note += "; "
		}
		note += "ended: " + res.ErrKind
	}
	row := &ChesterQuery{
		Ride: cx.Ride, AskedAt: utcString(now), TZ: cx.TZ, Mode: mode, Preset: preset, Question: question,
		PromptVersion: chesterPromptVersion, Model: m.Model, ServedModel: res.Served, Effort: m.Effort,
		FirstMS: res.FirstMS, TotalMS: res.TotalMS, TokensIn: res.Usage.In, TokensOut: res.Usage.Out,
		CacheRead: res.Usage.CacheRead, CacheWrite: res.Usage.CacheWrite, Searches: res.Usage.Searches,
		USD: usd, Answer: res.Answer, Places: res.Places, StopReason: res.Stop,
		Error: a.chester.stripKey(res.Err), Note: a.chester.stripKey(note), PosSource: "none",
	}
	if cx.Pos != nil {
		row.PosLat, row.PosLng, row.PosSource, row.PosLabel = &cx.Pos.Lat, &cx.Pos.Lng, cx.Pos.Source, cx.Pos.Label
	}
	if err := a.store.InsertChesterQuery(row); err != nil {
		log.Printf("ERROR chester: the question could not be logged: %v", err)
		emit(chesterEvent{T: "error", D: "The answer could not be written to the log."})
	}
	// What happened and what it cost. Never the question, the answer or a place.
	log.Printf("chester: a %s question ended (%s) in %d ms, first word at %d ms, %d search(es), %d place(s), %s",
		mode, firstOf(res.ErrKind, res.Stop, "no stop reason"), res.TotalMS, res.FirstMS,
		res.Usage.Searches, len(res.Places), usd4(usd))

	if len(res.Places) > 0 {
		emit(chesterEvent{T: "places", Places: chesterCards(res.Places)})
	}
	if res.Err != "" {
		emit(chesterEvent{T: "error", D: row.Error})
	}
	emit(chesterEvent{T: "done", ID: row.ID, Stop: firstOf(res.Stop, res.ErrKind), USD: usd, MS: res.TotalMS})
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- the log -----------------------------------------------------------------------------

// chesterLogLimit is how many rows the page lists. The summary and the JSON
// export are always over the whole log.
const chesterLogLimit = 200

// What the log page may say about a verdict that was not saved. A code in the
// redirect, a fixed sentence on the page, as everywhere else on this site.
const (
	chErrForm    = "form"
	chErrMissing = "missing"
	chErrVerdict = "verdict"
	chErrChecked = "checked"
	chErrNote    = "note-long"
	chErrPair    = "pair"
	chErrNumber  = "number"
	chErrUnit    = "unit"
	chErrFailed  = "failed"
	chOKSaved    = "saved"
)

var chesterRefusals = map[string]string{
	chErrForm:    "the form could not be read",
	chErrMissing: "the log has no such question",
	chErrVerdict: "a verdict is right, partly, wrong or unverified",
	chErrChecked: "that is not one of the things an answer can be checked against",
	chErrNote:    "the note is longer than " + strconv.Itoa(maxChesterNote) + " bytes",
	chErrPair:    "a number pair needs both figures: Chester's and the reference",
	chErrNumber:  "a figure must be a plain number",
	chErrUnit:    "a number pair needs a unit: minutes, dollars or miles",
	chErrFailed:  "the verdict could not be saved",
}

var chesterNotices = map[string]string{chOKSaved: "Verdict saved."}

const chesterPricesNote = "Dollars are worked out from each response's own token counts at list prices, " +
	"plus web searches at $10 per 1,000. A model the price table does not know is priced as the dearest one."

// chesterLog is the experiment's page: the summary by mode and verdict, then
// every question with its figures, its answer, its places and the form that
// rules on it in place.
func (a *App) chesterLog(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.ChesterQueries(0)
	if err != nil {
		log.Printf("ERROR chester log: %v", err)
		http.Error(w, "the log could not be read", http.StatusInternalServerError)
		return
	}
	sum := summarizeChester(rows)
	shown := rows
	if len(shown) > chesterLogLimit {
		shown = shown[:chesterLogLimit]
	}
	a.render(w, r, "chester_log.html", map[string]any{
		"Title": "Chester log", "Rows": shown, "Sum": sum, "Hidden": len(rows) - len(shown),
		"Verdicts": chesterVerdicts, "Checks": chesterChecks, "Units": chesterUnits,
		"PricesNote": chesterPricesNote,
		"Error":      chesterRefusals[r.URL.Query().Get("error")],
		"Notice":     chesterNotices[r.URL.Query().Get("ok")],
	})
}

// chesterLogJSON is the whole log as data, for the signed-in trip-keeper and
// the push token only.
func (a *App) chesterLogJSON(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.ChesterQueries(0)
	if err != nil {
		http.Error(w, "the log could not be read", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"prompt_version": chesterPromptVersion, "prices_note": chesterPricesNote,
		"summary": summarizeChester(rows), "queries": rows,
	})
}

// formNumber reads an optional figure. ok is false only for something that
// was sent and is not a plain number.
func formNumber(r *http.Request, name string) (v *float64, ok bool) {
	s := strings.TrimSpace(r.PostFormValue(name))
	if s == "" {
		return nil, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1e9 {
		return nil, false
	}
	return &f, true
}

// chesterVerdict rules on one answer. POST /admin/chester/log/{id}.
//
//	verdict=right|partly|wrong|unverified
//	against=navigation|booking|map|ground     what it was checked against, optional
//	note=<text>                               optional
//	chester_value=, reference_value=, unit=   an optional number pair, in minutes, dollars or miles
//
// A verdict can be changed: the row keeps the latest. The page's form is
// answered with a redirect back to the row; curl gets JSON.
func (a *App) chesterVerdict(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	anchor := "#q-" + strconv.FormatInt(id, 10)
	html := wantsHTML(r)
	refuse := func(status int, code string) {
		if html {
			http.Redirect(w, r, "/admin/chester/log?error="+url.QueryEscape(code)+anchor, http.StatusSeeOther)
			return
		}
		writeJSON(w, status, map[string]any{"status": "refused", "error": chesterRefusals[code]})
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		refuse(http.StatusBadRequest, chErrForm)
		return
	}
	if _, err := a.store.ChesterQuery(id); err != nil {
		refuse(http.StatusNotFound, chErrMissing)
		return
	}
	verdict := strings.TrimSpace(r.PostFormValue("verdict"))
	if _, ok := chesterLabel(chesterVerdicts, verdict); !ok {
		refuse(http.StatusBadRequest, chErrVerdict)
		return
	}
	against := strings.TrimSpace(r.PostFormValue("against"))
	if _, ok := chesterLabel(chesterChecks, against); !ok && against != "" {
		refuse(http.StatusBadRequest, chErrChecked)
		return
	}
	note := strings.TrimSpace(r.PostFormValue("note"))
	if len(note) > maxChesterNote {
		refuse(http.StatusBadRequest, chErrNote)
		return
	}
	cv, ok1 := formNumber(r, "chester_value")
	rv, ok2 := formNumber(r, "reference_value")
	unit := strings.TrimSpace(r.PostFormValue("unit"))
	switch {
	case !ok1 || !ok2:
		refuse(http.StatusBadRequest, chErrNumber)
		return
	case (cv == nil) != (rv == nil):
		refuse(http.StatusBadRequest, chErrPair)
		return
	case cv == nil:
		unit = ""
	default:
		known := false
		for _, u := range chesterUnits {
			known = known || u == unit
		}
		if !known {
			refuse(http.StatusBadRequest, chErrUnit)
			return
		}
	}
	if err := a.store.SetChesterVerdict(id, verdict, against, note, cv, rv, unit, time.Now()); err != nil {
		log.Printf("ERROR chester verdict: %v", err)
		refuse(http.StatusInternalServerError, chErrFailed)
		return
	}
	// The verdict and nothing of the note.
	log.Printf("chester: a question was ruled %s", verdict)
	if html {
		http.Redirect(w, r, "/admin/chester/log?ok="+chOKSaved+anchor, http.StatusSeeOther)
		return
	}
	row, _ := a.store.ChesterQuery(id)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "query": row})
}
