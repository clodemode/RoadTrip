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
	{"onway", "Things to do on the way today", "What are some good things to do or see on the way today, close to today's road? For each, say roughly how many extra minutes it adds."},
	{"sleep", "Where to sleep tonight", "Where should we sleep tonight? Compare tonight's planned Stop with one or two towns a little before and after it on the same road, and say which have decent lodging inside our price band."},
	{"howfar", "How far to tonight", "How far is it to tonight's stop, and roughly how long will it take? Say that it is an estimate."},
	{"podcast", "A podcast for this stretch", "Find one podcast episode about the country we are driving through now, or will reach in the next two hours."},
	{chesterReroutePreset, "Change the route", "Weigh the route choices still open from here, and propose one switch if it is better. If none is open, say plainly that none is open and why: every other choice already driven, or the trip has no route choices."},
}

// chesterReroutePreset is the one preset that is offered only when the active
// trip has route choices; see offeredPresets.
const chesterReroutePreset = "reroute"

// offeredPresets is the presets the Chester page shows: all of them, and the
// route change only when the active trip has route choices to switch among.
func (a *App) offeredPresets() []struct{ ID, Label, Question string } {
	if a.hasRouteChoices() {
		return chesterPresets
	}
	var out []struct{ ID, Label, Question string }
	for _, p := range chesterPresets {
		if p.ID != chesterReroutePreset {
			out = append(out, p)
		}
	}
	return out
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

// chesterSaidOverLimit is the refusal when a question could take the day's
// spending past the limit. It names what is left, what this question could
// cost, and whether the cheaper mode would still fit.
func chesterSaidOverLimit(cfg *chesterConfig, mode string, spent float64) string {
	left := cfg.DailyUSD - spent
	if left <= 0 {
		return fmt.Sprintf("Chester has reached today's limit: %s of %s spent. It starts again at midnight UTC.",
			usd2(spent), usd2(cfg.DailyUSD))
	}
	said := fmt.Sprintf("Chester has %s left of today's limit of %s, and a %s question can cost up to %s, so it was not asked.",
		usd2(left), usd2(cfg.DailyUSD), mode, usd2(cfg.mode(mode).worstUSD()))
	if quick := cfg.Quick.worstUSD(); mode == chesterPlan && !chesterOverLimit(spent, quick, cfg.DailyUSD) {
		said += fmt.Sprintf(" A quick question, at up to %s, still fits.", usd2(quick))
	}
	return said + " The limit starts again at midnight UTC."
}

// chesterOverLimit is the rule: a question is refused when the day's
// spending plus the most it could cost is above the limit. The hair's width
// keeps two sums that should be equal from differing in the last bit.
func chesterOverLimit(spent, worst, limit float64) bool {
	return spent+worst > limit+1e-9
}

func usd2(v float64) string { return "$" + strconv.FormatFloat(v, 'f', 2, 64) }
func usd4(v float64) string { return "$" + strconv.FormatFloat(v, 'f', 4, 64) }

// chesterPage is the helper itself: the presets, the box, the switch, the
// answer and its cards. With no key it says so and offers nothing to press.
func (a *App) chesterPage(w http.ResponseWriter, r *http.Request) {
	a.renderChester(w, r, nil)
}

// renderChester draws the Chester page; extra carries a refused preferences
// form back to the page with what was typed and which field was wrong.
func (a *App) renderChester(w http.ResponseWriter, r *http.Request, extra map[string]any) {
	data := map[string]any{"Title": "Chester", "On": a.chester != nil, "Presets": a.offeredPresets()}
	if q := r.URL.Query().Get("q"); len(q) <= 1000 {
		data["Prefill"] = q // the template sets it as text
	}
	// A link to /chester?preset=<id> opens the page with that question in the box.
	if q, ok := chesterPreset(r.URL.Query().Get("preset")); ok && q != "" {
		for _, p := range a.offeredPresets() {
			if p.Question == q {
				data["Prefill"] = q
			}
		}
	}
	if a.chester != nil {
		cfg := a.chester.cfg
		spent, _ := a.store.ChesterSpent(time.Now())
		data["Spent"], data["Cap"] = usd2(spent), usd2(cfg.DailyUSD)
		data["Quick"], data["Plan"] = cfg.Quick, cfg.Plan
		data["QuickSecs"], data["PlanSecs"] = int(cfg.Quick.Timeout.Seconds()), int(cfg.Plan.Timeout.Seconds())
	}
	if _, typed := extra["Prefs"]; !typed {
		data["Prefs"] = a.loadPrefs().withDefaults()
	}
	for k, v := range extra {
		data[k] = v
	}
	data["PrefsSaved"] = r.URL.Query().Get("saved") == "preferences"
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
// A question that is refused before it is sent (no key, busy, nothing asked,
// or the day's limit: what is spent today plus the most this question could
// cost is above it) is answered with a status and a JSON body carrying one
// sentence, and no row is written: nothing was spent.
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
	// Drive Tracks (source=drivetracks) is one fixed tap: the question is built
	// here, always quick, never one of the preset buttons.
	drive := strings.TrimSpace(r.PostFormValue("source")) == chesterSourceDrive
	if drive && a.tracks == nil {
		refuse(http.StatusServiceUnavailable, driveTracksSaidDown)
		return
	}
	mode := strings.TrimSpace(r.PostFormValue("mode"))
	if drive {
		mode = chesterQuick
	}
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
	var ask driveAsk
	if drive {
		preset = chesterDriveTracksPreset
		ask = a.driveTracksQuestion(a.userOf(r), time.Now())
		question = ask.question
	} else if preset != "" {
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
	case len(question) > maxChesterQuestion && !drive: // the site's own words for a tap are not the traveller's to keep short
		refuse(http.StatusBadRequest, chesterSaidLong)
		return
	}
	// Half a coordinate is no coordinate.
	lat, lng := formCoord(r, "lat", 90), formCoord(r, "lng", 180)
	if lat == nil || lng == nil {
		lat, lng = nil, nil
	}
	// No phone position and a simulated drive running: the simulated phone
	// answers, through the same publishPosition coarsening below.
	if lat == nil {
		lat, lng = a.simFix(r)
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
	m := cfg.mode(mode)
	// The limit is checked against the most this question could cost, not
	// against nothing: checked only at zero, one long question could take
	// the day well past it.
	if worst := m.worstUSD(); chesterOverLimit(spent, worst, cfg.DailyUSD) {
		log.Printf("chester: refused, a %s question could pass the day's limit (%s of %s spent, up to %s more)",
			mode, usd2(spent), usd2(cfg.DailyUSD), usd2(worst))
		refuse(http.StatusTooManyRequests, chesterSaidOverLimit(cfg, mode, spent))
		return
	}
	cx, err := a.chesterContext(now, lat, lng)
	if err != nil {
		log.Printf("ERROR chester: reading the ride: %v", err)
		refuse(http.StatusInternalServerError, chesterSaidNoRide)
		return
	}

	// Every call says which drive it is part of, and what Chester remembers
	// of this traveller: both after the cache breakpoint, in the volatile part.
	user := a.userOf(r)
	cx.Volatile += a.driveNote(r.Context(), user, now, lat, lng) + a.memoryNote(user)

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

	// The music library is offered only when RoadTracks is on.
	var lib *chesterMusicLib
	if a.tracks != nil {
		lib = &chesterMusicLib{svc: a.tracks, melt: a.melt, one: drive, avoid: ask.avoid, artists: ask.artists}
	}
	// Podcasts are offered on every question but Drive Tracks, which asks for one album.
	if a.podcasts != nil && !drive {
		cx.podcasts = &chesterPodcastLib{svc: a.podcasts, melt: a.melt}
	}
	res := a.chester.ask(r.Context(), mode, cx, question, lib, emit)
	if drive {
		a.driveTracksMelt(ask, res)
	}
	if res.Err == "" && !res.Synth && res.Answer != "" && !drive {
		a.remember(user, question, res.Answer, res.Leanings, now)
	}
	// The row keeps the API's own usage for every request, and its tokens,
	// searches and dollars are read back out of exactly what it keeps.
	usageJSON := chesterUsageJSON(res.Requests)
	used, err := chesterUsageFromJSON(usageJSON)
	if err != nil {
		log.Printf("ERROR chester: the usage record could not be read back: %v", err)
		used = chesterUsageOf(res.Requests)
	}
	usd, estimated := used.cost(), used.CeilingUSD > 0
	notes := res.Notes
	if estimated {
		notes = append(notes, "the cost is an estimate: a round was cut off before the API said what it used, so "+
			usd4(used.CeilingUSD)+" was charged for it, the most it could have written")
	}
	if res.ErrKind != "" {
		notes = append(notes, "ended: "+res.ErrKind)
	}
	note := strings.Join(notes, "; ")
	row := &ChesterQuery{
		User: user, Ride: cx.Ride, AskedAt: utcString(now), TZ: cx.TZ, Mode: mode, Preset: preset, Question: question,
		PromptVersion: chesterPromptVersion, Model: m.Model, ServedModel: res.Served, Effort: m.Effort,
		TotalMS: res.TotalMS, TokensIn: used.In, TokensOut: used.Out,
		CacheRead: used.CacheRead, CacheWrite: used.CacheWrite, Searches: used.Searches,
		USD: usd, USDEstimated: estimated, UsageJSON: json.RawMessage(usageJSON),
		Answer: res.Answer, AnswerSynth: res.Synth, Partial: res.Partial, Places: res.Places, Music: res.Music, Podcasts: res.Podcasts, StopReason: res.Stop,
		Error: a.chester.stripKey(res.Err), Note: a.chester.stripKey(note), PosSource: "none",
	}
	// A first-word time is a time a word of the model's arrived. With no
	// word there is none, and the row holds nothing rather than a zero.
	firstWord := "no words"
	if res.FirstMS > 0 {
		row.FirstMS = &res.FirstMS
		firstWord = "first word at " + strconv.FormatInt(res.FirstMS, 10) + " ms"
	}
	if cx.Pos != nil {
		row.PosLat, row.PosLng, row.PosSource, row.PosLabel = &cx.Pos.Lat, &cx.Pos.Lng, cx.Pos.Source, cx.Pos.Label
	}
	if res.Proposal != nil && res.Err == "" {
		// The row says what was proposed; what became of it is not recorded here.
		row.Proposal, _ = json.Marshal(map[string]string{"option": res.Proposal.Option, "choice": res.Proposal.Choice,
			"why": res.Proposal.Why, "outcome": "proposed"})
	}
	if err := a.store.InsertChesterQuery(row); err != nil {
		log.Printf("ERROR chester: the question could not be logged: %v", err)
		emit(chesterEvent{T: "error", D: "The answer could not be written to the log."})
	}
	// What happened and what it cost. Never the question, the answer or a place.
	log.Printf("chester: a %s question ended (%s) in %d ms, %s, %d search(es), %d place(s), %s",
		mode, firstOf(res.ErrKind, res.Stop, "no stop reason"), res.TotalMS, firstWord,
		used.Searches, len(res.Places), usd4(usd))

	if len(res.Places) > 0 {
		emit(chesterEvent{T: "places", Places: chesterCards(res.Places)})
	}
	if len(res.Music) > 0 {
		emit(chesterEvent{T: "music", Music: res.Music})
	}
	if len(res.Podcasts) > 0 {
		emit(chesterEvent{T: "podcasts", Podcasts: res.Podcasts})
	}
	if drive && len(res.Music) == 0 && res.MusicDown && res.Err == "" {
		emit(chesterEvent{T: "error", D: driveTracksSaidDown})
	}
	if res.Proposal != nil && res.Err == "" {
		res.Proposal.QueryID = row.ID
		emit(chesterEvent{T: "proposal", Proposal: res.Proposal})
	}
	if res.Err != "" {
		// Drop: whatever words were streamed before this are not an answer,
		// and the page takes them down and shows this sentence instead.
		emit(chesterEvent{T: "error", D: row.Error, Drop: true})
	}
	emit(chesterEvent{T: "done", ID: row.ID, Stop: firstOf(res.Stop, res.ErrKind), USD: usd, MS: res.TotalMS, Est: estimated})
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
	"plus web searches at $10 per 1,000. Each row keeps those counts as the API sent them (usage_json in log.json), " +
	"and its tokens and dollars are worked out from them. A model the price table does not know is priced as the dearest one. " +
	"A question that was cut off before the API said what it used is charged the most it could have written, " +
	"and its cost is marked as an estimate."

// chesterLog is the experiment's page: the summary by mode and verdict, then
// every question with its figures, its answer, its places and the form that
// rules on it in place.
func (a *App) chesterLog(w http.ResponseWriter, r *http.Request) {
	user := a.userOf(r)
	rows, err := a.store.ChesterQueriesOf(user)
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
	var others []chesterOther
	if a.auth.IsAdmin(r) {
		others, _ = a.store.ChesterOthers(user)
	}
	a.render(w, r, "chester_log.html", map[string]any{
		"Others": others,
		"Title":  "Chester log", "Rows": shown, "Sum": sum, "Hidden": len(rows) - len(shown),
		"Verdicts": chesterVerdicts, "Checks": chesterChecks, "Units": chesterUnits,
		"PricesNote": chesterPricesNote,
		"Error":      chesterRefusals[r.URL.Query().Get("error")],
		"Notice":     chesterNotices[r.URL.Query().Get("ok")],
	})
}

// chesterLogJSON is the whole log as data, for the signed-in trip-keeper and
// the push token only.
func (a *App) chesterLogJSON(w http.ResponseWriter, r *http.Request) {
	user := a.userOf(r)
	rows, err := a.store.ChesterQueriesOf(user)
	if err != nil {
		http.Error(w, "the log could not be read", http.StatusInternalServerError)
		return
	}
	others := []chesterOther{}
	if a.auth.IsAdmin(r) {
		others, _ = a.store.ChesterOthers(user)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"others":         others,
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

// chesterVerdict rules on one answer. POST /driver/chester/log/{id}.
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
			http.Redirect(w, r, "/driver/chester/log?error="+url.QueryEscape(code)+anchor, http.StatusSeeOther)
			return
		}
		writeJSON(w, status, map[string]any{"status": "refused", "error": chesterRefusals[code]})
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		refuse(http.StatusBadRequest, chErrForm)
		return
	}
	// Only the driver who asked may rule: to rule is to read the answer.
	if q, err := a.store.ChesterQuery(id); err != nil || q.User == "" || q.User != a.userOf(r) {
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
		http.Redirect(w, r, "/driver/chester/log?ok="+chOKSaved+anchor, http.StatusSeeOther)
		return
	}
	row, _ := a.store.ChesterQuery(id)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "query": row})
}
