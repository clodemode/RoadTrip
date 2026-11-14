package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// initCombinationCapScenarios binds features/trip_document_combination_cap.feature.
// The options are synthetic: invented ids and labels, no places.
func initCombinationCapScenarios(sc *godog.ScenarioContext) {
	var (
		opts []tripOption
		sels []map[string]string
		err  error
	)
	choices := func(n int) []tripChoice {
		out := make([]tripChoice, n)
		for i := range out {
			out[i] = tripChoice{ID: "c" + strconv.Itoa(i), Label: "Choice " + strconv.Itoa(i)}
		}
		return out
	}
	sc.Step(`^a synthetic document with a south option of 5 choices, three dependent options of 2 choices each that apply only under the classic south, and an independent option of 2 choices$`, func() error {
		south := choices(5)
		south[0].ID = "classic"
		when := map[string]string{"south": "classic"}
		opts = []tripOption{
			{ID: "south", Default: "classic", Choices: south},
			{ID: "day1", Default: "c0", Choices: choices(2), When: when},
			{ID: "night1", Default: "c0", Choices: choices(2), When: when},
			{ID: "day2", Default: "c0", Choices: choices(2), When: when},
			{ID: "morning", Default: "c0", Choices: choices(2)},
		}
		return nil
	})
	sc.Step(`^a synthetic document with independent options of (\d+) and (\d+) choices$`, func(a, b int) error {
		opts = []tripOption{{ID: "a", Default: "c0", Choices: choices(a)}, {ID: "b", Default: "c0", Choices: choices(b)}}
		return nil
	})
	sc.Step(`^the combinations are counted$`, func() error { sels, err = allSelections(opts); return nil })
	sc.Step(`^the document is accepted with (\d+) plans$`, func(n int) error {
		if err != nil {
			return fmt.Errorf("refused: %v", err)
		}
		if len(sels) != n {
			return fmt.Errorf("%d plans, want %d", len(sels), n)
		}
		return nil
	})
	sc.Step(`^the document is refused as offering more than 64 combinations$`, func() error {
		if err == nil || !strings.Contains(err.Error(), "more than 64 combinations") {
			return fmt.Errorf("want a refusal over 64, got %v", err)
		}
		return nil
	})
}
