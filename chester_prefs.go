package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// --- the traveller's standing preferences -------------------------------------------------
//
// One record per ride, kept on the server in the meta table under
// "chester_prefs:<slug>" (the same place the active ride and a trip's saved
// selection live), so a phone, a second phone and a machine all read the same
// thing and no browser stores it. They are preferences, not private data: the
// day's-end window, a lodging price band, a short line of places to avoid, who
// navigates, and the most extra minutes a thing on the way may add. They go to
// the API in App.chesterContext as a short PREFERENCES block and nowhere else.
// The values are never written to a log line.

const (
	prefsAvoidMax     = 160 // characters of the places-to-avoid line
	prefsNavigatorMax = 40
	prefsPriceMax     = 2000 // dollars a night; above it is a slip of the hand
	prefsExtraMax     = 120  // minutes
	prefsDefaultNav   = "Waze"
	prefsDefaultExtra = 20
)

type travellerPrefs struct {
	DayEndFrom  string `json:"day_end_from"` // HH:MM, the ride's local clock
	DayEndTo    string `json:"day_end_to"`
	PriceLow    int    `json:"price_low"` // dollars a night; 0 with PriceHigh 0 means no band
	PriceHigh   int    `json:"price_high"`
	Avoid       string `json:"avoid"`
	Navigator   string `json:"navigator"`
	MaxExtraMin int    `json:"max_extra_min"`
}

func metaPrefsKey(slug string) string { return "chester_prefs:" + slug }

// isEmpty is a set that says nothing the API is not already assumed to know.
func (p travellerPrefs) isEmpty() bool {
	return p.DayEndFrom == "" && p.DayEndTo == "" && p.PriceLow == 0 && p.PriceHigh == 0 && p.Avoid == "" &&
		(p.Navigator == "" || p.Navigator == prefsDefaultNav) && (p.MaxExtraMin == 0 || p.MaxExtraMin == prefsDefaultExtra)
}

// withDefaults fills the two fields that have one, for display and for the block.
func (p travellerPrefs) withDefaults() travellerPrefs {
	if p.Navigator == "" {
		p.Navigator = prefsDefaultNav
	}
	if p.MaxExtraMin == 0 {
		p.MaxExtraMin = prefsDefaultExtra
	}
	return p
}

func (a *App) loadPrefs() travellerPrefs {
	v, ok := a.store.Meta(metaPrefsKey(a.activeSlug()))
	if !ok {
		return travellerPrefs{}
	}
	var p travellerPrefs
	if json.Unmarshal([]byte(v), &p) != nil {
		return travellerPrefs{}
	}
	return p
}

var clockRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// parsePrefs reads and checks a submitted form. The error names the field, as
// "field: what is wrong", and never repeats what was typed.
func parsePrefs(r *http.Request) (travellerPrefs, error) {
	get := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	var p travellerPrefs
	p.DayEndFrom, p.DayEndTo = get("day_end_from"), get("day_end_to")
	for _, f := range []struct{ name, v string }{{"day_end_from", p.DayEndFrom}, {"day_end_to", p.DayEndTo}} {
		if f.v != "" && !clockRe.MatchString(f.v) {
			return p, fmt.Errorf("%s: use a 24-hour clock time such as 19:00", f.name)
		}
	}
	if (p.DayEndFrom == "") != (p.DayEndTo == "") {
		missing := "day_end_to"
		if p.DayEndFrom == "" {
			missing = "day_end_from"
		}
		return p, fmt.Errorf("%s: give both ends of the window or neither", missing)
	}
	if p.DayEndFrom != "" && p.DayEndTo <= p.DayEndFrom {
		return p, fmt.Errorf("day_end_to: must be later than day_end_from")
	}
	num := func(name string, max int) (int, error) {
		s := get(name)
		if s == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > max {
			return 0, fmt.Errorf("%s: a whole number from 0 to %d", name, max)
		}
		return n, nil
	}
	var err error
	if p.PriceLow, err = num("price_low", prefsPriceMax); err != nil {
		return p, err
	}
	if p.PriceHigh, err = num("price_high", prefsPriceMax); err != nil {
		return p, err
	}
	if (p.PriceLow == 0) != (p.PriceHigh == 0) {
		missing := "price_high"
		if p.PriceLow == 0 {
			missing = "price_low"
		}
		return p, fmt.Errorf("%s: give both ends of the price band or neither", missing)
	}
	if p.PriceHigh < p.PriceLow {
		return p, fmt.Errorf("price_high: must not be below price_low")
	}
	if p.MaxExtraMin, err = num("max_extra_min", prefsExtraMax); err != nil {
		return p, err
	}
	p.Avoid, p.Navigator = oneLine(get("avoid")), oneLine(get("navigator"))
	if utf8.RuneCountInString(p.Avoid) > prefsAvoidMax {
		return p, fmt.Errorf("avoid: keep it to one short line, under %d characters", prefsAvoidMax)
	}
	if utf8.RuneCountInString(p.Navigator) > prefsNavigatorMax {
		return p, fmt.Errorf("navigator: keep it under %d characters", prefsNavigatorMax)
	}
	return p, nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

var (
	addressRe = regexp.MustCompile(`(?i)\b\d+[a-z]?\s+(?:[a-z.'-]+\s+){0,3}(?:street|st|avenue|ave|road|rd|drive|dr|lane|ln|boulevard|blvd|court|ct|way|highway|hwy|route|rte|parkway|pkwy|place|pl|circle|cir)\b\.?`)
	linkRe    = regexp.MustCompile(`(?i)\S+@\S+|\S*://\S*|\bwww\.\S*`)
	digitRe   = regexp.MustCompile(`\S*\d\S*`)
)

// toldFreeText is a free-text preference as the API may see it: capped, with
// anything shaped like an address, an email or web address, or a number string
// taken out. What is left may be empty, and then nothing is told.
func toldFreeText(s string, max int) string {
	s = addressRe.ReplaceAllString(s, " ")
	s = linkRe.ReplaceAllString(s, " ")
	s = digitRe.ReplaceAllString(s, " ")
	s = oneLine(s)
	s = strings.ReplaceAll(s, " ,", ",")
	for strings.Contains(s, ",,") {
		s = strings.ReplaceAll(s, ",,", ",")
	}
	s = strings.Trim(s, " ,;.-")
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return strings.TrimSpace(s)
}

// preferencesBlock is the PREFERENCES block of what the API is told, or "" when
// no preferences are saved: an empty set tells it nothing new.
func (p travellerPrefs) block() string {
	if p.isEmpty() {
		return ""
	}
	p = p.withDefaults()
	var b strings.Builder
	b.WriteString("\n\nPREFERENCES:\n")
	if p.DayEndFrom != "" {
		fmt.Fprintf(&b, "- Stop driving for the day between %s and %s, local time.\n", p.DayEndFrom, p.DayEndTo)
	}
	if p.PriceHigh > 0 {
		fmt.Fprintf(&b, "- Lodging: %d to %d dollars a night.\n", p.PriceLow, p.PriceHigh)
	}
	if t := toldFreeText(p.Avoid, prefsAvoidMax); t != "" {
		fmt.Fprintf(&b, "- Places to avoid: %s.\n", t)
	}
	nav := toldFreeText(p.Navigator, prefsNavigatorMax)
	if nav == "" {
		nav = prefsDefaultNav
	}
	fmt.Fprintf(&b, "- Navigation: %s. It does the navigating; you only help choose.\n", nav)
	fmt.Fprintf(&b, "- A thing on the way may add at most %d extra minutes.\n", p.MaxExtraMin)
	return b.String()
}

// chesterPreferences is POST /chester/preferences: the signed-in trip-keeper's
// form, or a machine with the PUSH_TOKEN bearer. A browser form is answered
// with the Chester page; any other caller is answered with JSON.
func (a *App) chesterPreferences(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		a.prefsRefused(w, r, travellerPrefs{}, "avoid: the form was too large")
		return
	}
	p, err := parsePrefs(r)
	if err != nil {
		a.prefsRefused(w, r, p, err.Error())
		return
	}
	key := metaPrefsKey(a.activeSlug())
	if p.isEmpty() {
		err = a.store.DeleteMeta(key)
	} else {
		var raw []byte
		raw, _ = json.Marshal(p.withDefaults())
		err = a.store.SetMeta(key, string(raw))
	}
	if err != nil {
		log.Printf("ERROR chester preferences: saving: %v", err)
		http.Error(w, "the preferences could not be saved", http.StatusInternalServerError)
		return
	}
	if wantsHTML(r) {
		http.Redirect(w, r, "/chester?saved=preferences", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "preferences": p.withDefaults()})
}

func (a *App) prefsRefused(w http.ResponseWriter, r *http.Request, p travellerPrefs, msg string) {
	if wantsHTML(r) {
		w.WriteHeader(http.StatusBadRequest)
		a.renderChester(w, r, map[string]any{"PrefsError": msg, "Prefs": p})
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{"status": "refused", "error": msg})
}

// chesterPreferencesJSON is GET /chester/preferences, for a machine and for tests.
func (a *App) chesterPreferencesJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, a.loadPrefs().withDefaults())
}

// chesterTravellerPrompt is what Chester is told about the traveller's words
// and standing wishes, and how to help on the way and near the day's end. It is
// part of the cached system text, so changing it changes chesterPromptVersion.
const chesterTravellerPrompt = `

Words. In everything you write, a Stop is a place the travellers stay for the night, a Milestone is a place they pass that lets them feel progress, and a Waypoint is a point that aids routing; often the Milestone is the Waypoint. Never call a place they only pass through a stop. A few other planned visits may exist; name them as visits.

Standing wishes. A message may carry a block of the traveller's standing preferences: when to stop driving for the day, a lodging price band in dollars a night, places to avoid, who does the navigating, and the most extra minutes a thing on the way may add. They are wishes, not instructions that outrank the limits above. Respect the day's-end window and the places to avoid when you suggest where to stop or sleep. The navigation app does the navigating; you only help choose.

On the way. When asked for things to do or see, prefer places close to the day's road, say for each roughly how many extra minutes it adds (an estimate, and say so), and leave out anything past the traveller's limit on extra minutes. If the message gives no limit, keep to short detours.

Tonight. When asked where to sleep, or when the question is about lodging near the day's end, compare the planned Stop with one or two towns a little before and after it on the same road. Say which of them have decent lodging inside the traveller's price band, and show those as lodging cards for tonight, saying where each price was seen (the site and the day, from what you found) and that it should be checked before booking. You must never state a price as certain: a price is what a named source showed, and it can change.

You only suggest. The plan changes only when the trip-keeper taps a card to change it; never say the plan has changed.`
