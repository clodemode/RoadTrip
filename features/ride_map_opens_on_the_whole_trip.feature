Feature: The Ride map opens on the whole trip, destination included
  Followers watch the public Ride page from a phone. The map must open showing
  the whole planned route and what was driven, so the destination is on screen
  without zooming out. A finger that only touches the map to scroll the page, or
  a wheel turn over it, is not the visitor taking the view: the opening view stays
  fitted to the whole trip until the visitor really drags or zooms the map.

  Background:
    Given a ride of three days from "Start Town" to "Finish Town" where the first two days are driven

  Scenario: The destination is inside the opening view
    Given the map's box measures 2 by 380 when the page first runs
    When a finger touches the map while the page settles
    And the page finishes laying out at 375 by 380
    Then the destination is inside the opening view

  Scenario: The driven part and the whole planned route are inside the opening view
    Given the map's box measures 500 by 380 when the page first runs
    Then the start is inside the opening view
    And the driven part is inside the opening view
    And the destination is inside the opening view

  Scenario: A wheel turn over the map while the page settles does not freeze the view
    Given the map's box measures 2 by 520 when the page first runs
    When the wheel turns over the map while the page settles
    And the page finishes laying out at 1366 by 520
    Then the destination is inside the opening view

  Scenario: A visitor who moved the map keeps the view when the page resizes
    Given the map's box measures 375 by 380 when the page first runs
    When the visitor drags the map
    And the page finishes laying out at 700 by 520
    Then the zoom is the one the visitor had
