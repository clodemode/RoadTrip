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
				initDayPickScenarios(t, sc, mw)
				initFavouriteScenarios(t, sc)
				initPlanDaySummaryScenarios(t, sc, mw)
				initChesterMusicScenarios(t, sc)
				initChesterAnswerScenarios(t, sc)
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
				initDeleteRideScenarios(t, sc)
				initRoutingPointsScenarios(t, sc)
				initNightForkScenarios(t, sc)
				initPlanChoiceAfterDrivenDayScenarios(t, sc)
				initMenuStateScenarios(t, sc)
				initFinishCheckinScenarios(t, sc)
				initTestDriveSwitchScenarios(t, sc)
				initRidePickerScenarios(t, sc)
				initContinuousMusicScenarios(t, sc)
				initPlayerTroubleScenarios(t, sc)
				npw := initPublicNowPlayingScenarios(t, sc)
				initListenInnCardScenarios(t, sc, npw)
				initListenInnAboveMapScenarios(t, sc, npw)
				initListenInnScenarios(t, sc)
				initChesterPreferencesScenarios(t, sc)
				initChesterPositionHistoryScenarios(t, sc)
				initPhotoViewScenarios(sc, sw.p)
				initEveningPostScenarios(sc)
				initChesterCancelScenarios(t, sc)
				initCombinationCapScenarios(sc)
				initMapPolishScenarios(t, sc)
				initMapPhoneScenarios(t, sc)
				initFinderScenarios(t, sc)
				initFinishedTripScenarios(t, sc)
				initFuzzOneKilometreScenarios(sc)
				initTrailPointScenarios(t, sc)
				initPhotoMarkerScenarios(t, sc)
				initListenCountScenarios(t, sc)
				initStackedSetupScenarios(t, sc)
				initAuditSecurityScenarios(t, sc)
				initTimeFlowScenarios(t, sc)
				initNavigableScenarios(t, sc)
				initSocialCardScenarios(t, sc)
			}()
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    featurePaths(t),
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("a feature scenario failed")
	}
}
