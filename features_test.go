package main

import (
	"testing"

	"github.com/cucumber/godog"
)

// TestFeatures runs every Gherkin file under features/ against the real mux,
// a throwaway store and, where a step needs one, a stubbed Messages API and a
// fake Redis. The feature files are the statement of what the site does; the
// step bindings are in sim_steps_test.go.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			func() {
				sim := initSimScenarios(t, sc)
				initEtaScenarios(t, sc, sim)
				initPostScenarios(t, sc, sim)
				initMarksScenarios(t, sc, sim)
				initChesterMusicScenarios(t, sc)
				initDayBadgeScenarios(sc)
				initDriveTracksScenarios(t, sc)
				initStartButtonScenarios(sc)
				initRoadtracksPlayerScenarios(t, sc)
				initCanadianWeatherScenarios(t, sc)
				initTrafficChipBudgetScenarios(t, sc)
				initRouteMeHereScenarios(t, sc)
				initChesterRerouteScenarios(t, sc)
			}()
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("a feature scenario failed")
	}
}
