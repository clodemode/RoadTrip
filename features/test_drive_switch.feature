Feature: The trip-keeper swaps between trips, and the hours timeline is theirs
  The site shows one trip. A signed-in trip-keeper can swap it for a test drive
  and back from any page. The hours timeline is a planning aid on Plan, signed in.

  Background:
    Given two trips are on the volume, the second named "Test Drive"

  Scenario: A visitor sees no hours timeline anywhere
    When a visitor reads "/" and "/plan"
    Then neither page has an hours timeline

  Scenario: The trip-keeper sees the timeline on Plan and not on the Ride page
    When the trip-keeper reads "/plan?embed=1"
    Then the page has an hours timeline
    When the trip-keeper reads "/?embed=1"
    Then the page has no hours timeline

  Scenario: A visitor is offered no switch
    When a visitor reads "/plan"
    Then the page has no trip switch

  Scenario: The trip-keeper switches to the test drive and stays on the page
    When the trip-keeper reads "/driver?embed=1"
    Then the page offers a switch to "Test Drive"
    When the trip-keeper switches to "Test Drive" from "/plan"
    Then the answer returns to "/plan"
    And the site shows "Test Drive"
    When the trip-keeper switches back to the first trip from "/driver"
    Then the answer returns to "/driver"
    And the site no longer shows "Test Drive"
