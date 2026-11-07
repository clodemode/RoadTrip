Feature: ETA only while driving, in round minutes, and scored
  An ETA is a promise. The driver screen makes none before the car is moving,
  never makes one to the minute, and remembers every one it shows so the trip
  can say afterwards how good they were.

  Background:
    Given the site allows simulated drives
    And kris is signed in

  Scenario: No ETA before the car moves
    When the HUD is asked with no position
    Then the HUD shows no ETA

  Scenario: No ETA while parked on the route
    Given a fix on the first leg that is not moving
    When the HUD is asked with that fix
    Then the HUD shows no ETA

  Scenario: An ETA while driving, rounded to five minutes
    Given a moving fix on the first leg
    When the HUD is asked with that fix
    Then the HUD shows an ETA that begins with "about"
    And its minutes are a multiple of five
    And it is labelled plan pace

  Scenario: Off the planned route the HUD says so instead of guessing
    Given a moving fix 60 miles from every leg
    When the HUD is asked with that fix
    Then the HUD says it is off route and gives the distance to the plan in whole miles
    And the HUD shows no ETA and no miles done

  Scenario: Every ETA shown is remembered, once per five minutes per stop
    Given a moving fix on the first leg
    When the HUD is asked with that fix three times within one minute
    Then exactly one ETA prediction is recorded for the next stop

  Scenario: Arriving scores the predictions for that stop
    Given an ETA prediction for the next stop with predicted arrival T
    When kris marks the next stop arrived at T plus 12 minutes
    Then that prediction is scored with an error of 12 minutes
    And the bus carries an eta_hit record for that stop with the error in minutes
    And that record carries no latitude and no longitude

  Scenario: The driver can read the ETA ledger
    Given a scored ETA prediction exists
    When kris opens the ETA ledger
    Then it lists the prediction with its predicted time, actual time and error in minutes
    And it states the share of predictions within ten minutes

  Scenario: A FreeLance arrival is rounded the same way
    Given a Destined route whose arrival is 4:37 PM
    Then the Destined view shows "about 4:35 PM"
