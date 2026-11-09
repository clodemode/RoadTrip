package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// Chester's route choices: what it is told about the trip's planned
// alternatives, the one tool that proposes a switch, and the card the page
// draws from it. Chester proposes; the trip-keeper taps Save, which is the
// existing plan save (POST /driver/plan). Nothing here writes the plan.

// chesterProposeTool is the tool offered only when the trip has options.
const chesterProposeTool = "propose_route_choice"

// chesterProposalCard is the proposal as the page draws it. Selection is the
// saved choices with the one change, so Save posts the whole selection (the
// plan save is strict about what it is sent). Deltas are the plan's own
// estimate, signed, never traffic.
type chesterProposalCard struct {
	Option      string            `json:"option"`
	OptionTitle string            `json:"option_title"`
	Choice      string            `json:"choice"`
	ChoiceLabel string            `json:"choice_label"`
	Detail      string            `json:"detail,omitempty"`
	MilesDelta  string            `json:"miles_delta"`
	HoursDelta  string            `json:"hours_delta"`
	InProgress  bool              `json:"in_progress"`
	Why         string            `json:"why"`
	Selection   map[string]string `json:"selection"`
	Start       string            `json:"start"`
	QueryID     int64             `json:"query_id,omitempty"`
}

func chesterProposeToolDef() anthropic.BetaToolUnionParam {
	return clientTool(chesterProposeTool,
		"Propose switching one of the trip's planned route choices, shown as a card with a Save button that the trip-keeper taps. "+
			"You change nothing yourself. Give the option id, the choice id (both as listed under ROUTE CHOICES) and one short sentence on why. "+
			"Never propose a choice marked LOCKED or the one already selected. Call it at most once, then write the answer.",
		map[string]any{
			"option": map[string]any{"type": "string", "description": "An option id from ROUTE CHOICES."},
			"choice": map[string]any{"type": "string", "description": "A choice id of that option."},
			"why":    map[string]any{"type": "string", "description": "One short sentence."},
		}, []string{"option", "choice", "why"})
}

const chesterProposalShown = "The proposal card is on the page with a Save button. Write the whole answer now, " +
	"saying what the switch changes and that the trip-keeper decides by tapping Save. Do not call propose_route_choice again."

const chesterRoutePrompt = `

Route choices. When the ride offers choices (ROUTE CHOICES in the plan) and the question bears on the route, call propose_route_choice once, with the option id, the choice id and a one-sentence why. The page shows it as a card; the trip-keeper decides by tapping Save, and you change nothing. Never propose a LOCKED choice: say that road is already driven and offer Route me here on a place card instead. Do not propose switching an IN PROGRESS option unless the traveller asks outright. Miles and hours there are the plan's estimate, not traffic.`

func signed1(v float64) string {
	v = math.Round(v*10) / 10
	if v == 0 {
		v = 0
	}
	return fmt.Sprintf("%+.1f", v)
}

// routeChoices builds the ROUTE CHOICES block and keeps what the validator
// needs on cx. It is empty (and the tool is not offered) unless the active
// ride has a trip file with options. No coordinate and nothing private goes
// into it: ids, titles, labels, plan estimates and the guard's own reasons.
func (a *App) routeChoices(cx *chesterContext, legs []Leg, di dayInfo) string {
	st, err := a.loadPlan()
	if err != nil || !st.HasFile || len(st.Doc.Options) == 0 {
		return ""
	}
	saved, start := st.savedChoices(), st.savedStart()
	base := st.Doc.summarize(saved, start)
	cx.plan, cx.choices, cx.start = st, saved, start
	cx.locked, cx.started = map[string]string{}, map[string]bool{}

	// A drive under way: every option whose choice holds today's leg.
	if s := tripStart(legs); s != nil && s.ArrivedAt != "" && di.Today != nil {
		for _, l := range st.Doc.Legs {
			if l.Slug == di.Today.Slug {
				for k := range l.When {
					cx.started[k] = true
				}
			}
		}
	}

	var b strings.Builder
	b.WriteString("\n\nROUTE CHOICES. Switching one is proposed with propose_route_choice and decided by the trip-keeper's tap. " +
		"Figures are the plan's own estimate, not traffic.")
	force := st.Doc.inForce(saved)
	for _, o := range st.Doc.Options {
		if !force[o.ID] {
			continue // tied to a choice not selected: not on this plan
		}
		fmt.Fprintf(&b, "\nOption [%s] %s", o.ID, o.Title)
		if cx.started[o.ID] {
			b.WriteString(" - IN PROGRESS: the car is on this leg now.")
		}
		for _, c := range o.Choices {
			fmt.Fprintf(&b, "\n  - [%s] %s", c.ID, c.Label)
			if c.Detail != "" {
				fmt.Fprintf(&b, " (%s)", c.Detail)
			}
			if saved[o.ID] == c.ID {
				b.WriteString(": selected")
				continue
			}
			ch := map[string]string{}
			for k, v := range saved {
				ch[k] = v
			}
			ch[o.ID] = c.ID
			s := st.Doc.summarize(ch, start)
			fmt.Fprintf(&b, ": would change the whole trip by %s miles, %s hours (plan estimate, not traffic)",
				signed1(s.TotalMiles-base.TotalMiles), signed1(s.TotalHours-base.TotalHours))
			if why := a.routeLocked(st, start, ch); why != "" {
				cx.locked[o.ID+"/"+c.ID] = why
				b.WriteString(". LOCKED: " + why)
			}
		}
	}
	return b.String()
}

// routeLocked is the plan guard's first reason to refuse that selection, run
// read-only the way planSave would run it, or "" when it would take.
func (a *App) routeLocked(st *planState, start string, choices map[string]string) string {
	sd, err := parseSeedBytes(st.Raw, st.Ride.Slug, &Selection{StartDate: start, Choices: choices})
	if err == nil {
		err = checkPlan(sd)
	}
	if err != nil {
		return "the plan cannot take it"
	}
	reasons, err := a.store.PlanConflictsFor(sd)
	if err != nil {
		return "it could not be checked"
	}
	if len(reasons) > 0 {
		return reasons[0]
	}
	return ""
}

// checkProposal validates one propose_route_choice call against the plan it
// was told. It returns the card, or the plain reason it is dropped and the
// sentence the model is told.
func (cx *chesterContext) checkProposal(raw string) (*chesterProposalCard, string, string) {
	var in struct{ Option, Choice, Why string }
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return nil, "the call could not be read", "Give an option id, a choice id and a short why."
	}
	if cx.plan == nil {
		return nil, "the trip has no route choices", "The trip has no route choices. Do not call this tool."
	}
	st := cx.plan
	if !st.Doc.hasChoice(in.Option, in.Choice) {
		return nil, fmt.Sprintf("no such choice %q for option %q", in.Choice, in.Option),
			"There is no such choice. Use an option id and a choice id exactly as listed under ROUTE CHOICES."
	}
	if !st.Doc.inForce(cx.choices)[in.Option] {
		return nil, fmt.Sprintf("option %q is not on the plan under the current choices", in.Option),
			"That option is not on the plan under the current choices, so it was not listed. Propose only an option listed under ROUTE CHOICES."
	}
	if cx.choices[in.Option] == in.Choice {
		return nil, "that choice is already selected", "That choice is already selected, so nothing changes. Say so in the answer."
	}
	if why, ok := cx.locked[in.Option+"/"+in.Choice]; ok {
		return nil, "that choice is locked: " + why,
			"That choice is LOCKED: " + why + ". Say that road is already driven and offer Route me here on a place card instead."
	}
	why := cleanLine(in.Why, 300)
	if why == "" {
		return nil, "the proposal gave no reason", "Give a one-sentence why."
	}
	card := &chesterProposalCard{Option: in.Option, Choice: in.Choice, Why: why, Start: cx.start,
		InProgress: cx.started[in.Option], Selection: map[string]string{}}
	for k, v := range cx.choices {
		card.Selection[k] = v
	}
	card.Selection[in.Option] = in.Choice
	for _, o := range st.Doc.Options {
		if o.ID != in.Option {
			continue
		}
		card.OptionTitle = o.Title
		for _, c := range o.Choices {
			if c.ID == in.Choice {
				card.ChoiceLabel, card.Detail = c.Label, c.Detail
			}
		}
	}
	base := st.Doc.summarize(cx.choices, cx.start)
	to := st.Doc.summarize(card.Selection, cx.start)
	card.MilesDelta = signed1(to.TotalMiles - base.TotalMiles)
	card.HoursDelta = signed1(to.TotalHours - base.TotalHours)
	return card, "", chesterProposalShown
}
