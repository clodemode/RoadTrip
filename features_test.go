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
				sw := initPostScenarios(t, sc, sim)
				initRideNavScenarios(sc, sw.p)
				initPrivateNavScenarios(sc, sw.p)
				initPhysicsScenarios(t, sc, sw)
				initArrivalTimeScenarios(sc, sw)
				initOldPhotoScenarios(sc, sw)
				mw := initMarksScenarios(t, sc, sim)
				initHoursMilestoneScenarios(t, sc, mw)
				initPlanDaySummaryScenarios(t, sc, mw)
				initChesterMusicScenarios(t, sc)
				initChesterPodcastScenarios(t, sc)
				initDayBadgeScenarios(sc)
				initDriveTracksScenarios(t, sc)
				initStartButtonScenarios(sc)
				initRoadtracksPlayerScenarios(t, sc)
				initCanadianWeatherScenarios(t, sc)
				initTrafficChipBudgetScenarios(t, sc)
				initRouteMeHereScenarios(t, sc)
				initChesterRerouteScenarios(t, sc)
				initChangeTripScenarios(t, sc)
				initRoutingPointsScenarios(t, sc)
				initNightForkScenarios(t, sc)
				initPlanChoiceAfterDrivenDayScenarios(t, sc)
				initMenuStateScenarios(t, sc)
				initFinishCheckinScenarios(t, sc)
				initTestDriveSwitchScenarios(t, sc)
				initContinuousMusicScenarios(t, sc)
				initPlayerTroubleScenarios(t, sc)
				initPublicNowPlayingScenarios(t, sc)
				initListenInnScenarios(t, sc)
				initChesterPreferencesScenarios(t, sc)
				initChesterPositionHistoryScenarios(t, sc)
				initPhotoViewScenarios(sc, sw.p)
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
