Feature: Music notes show on the Ride map and take the tap
  Each track played with a published point is a music note on the Ride map. A
  note must be in the opening view, big enough to see and tap, and on top of a
  stop pin that sits on the same spot, because a track often starts at a stop.

  Background:
    Given a ride of three days from "Start Town" to "Finish Town" where the first two days are driven
    And three tracks played along the first two days
    And a track started at the stop "Mid Town"

  Scenario: Every music note is inside the opening view
    Given the map's box measures 2 by 380 when the page first runs
    When a finger touches the map while the page settles
    And the page finishes laying out at 375 by 380
    Then every music note is inside the opening view

  Scenario: A music note is big enough to see and tap
    Given the map's box measures 375 by 380 when the page first runs
    Then every music note is at least 28 pixels across

  Scenario: A music note is on top of a stop pin on the same spot
    Given the map's box measures 375 by 380 when the page first runs
    Then the music note at "Mid Town" is stacked above the stop pins

  Scenario: A track with no usable point draws no note
    Given a track played with no point
    And the map's box measures 375 by 380 when the page first runs
    Then 4 music notes are drawn
