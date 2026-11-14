Feature: Asking Chester and playing a track each drop a trail point where the car has been
  Kris, 2026-11-14: neither an ask nor a play left a mark on the map. Now each
  one, when the phone sent a position, drops one trail point: a point and a
  time on the ride, not a post. The point is published first, through the same
  seam as every other position, so a private place is its snapped cell and
  nothing raw is stored. The been-here line reads trail points beside the
  posts. All points here are synthetic.

  Background:
    Given the trip is active with Chester and RoadTracks on, and the driver is signed in

  Scenario: An ask with a position adds one trail point
    When the driver asks Chester with the phone at 41.2000, -101.2000
    Then the trail holds 1 point
    And trail point 1 is at 41.2, -101.2 from "chester"

  Scenario: A play with a position adds one trail point
    When the driver's player starts a track with the phone at 41.3000, -101.3000
    Then the trail holds 1 point
    And trail point 1 is at 41.3, -101.3 from "play"

  Scenario: An ask with the position off adds none
    When the driver asks Chester with the phone's position off
    Then the trail holds 0 points

  Scenario: A play with no position adds none
    When the driver's player starts a track with no phone position
    Then the trail holds 0 points

  Scenario: A point inside a private zone is stored as the snapped cell
    When the driver asks Chester with the phone at 40.1234, -100.4321
    Then the trail holds 1 point
    And trail point 1 is at 40.12, -100.43 from "chester"
    And no stored trail point carries the number "40.1234"

  Scenario: The map's trail includes them in time order
    When the driver asks Chester with the phone at 41.2000, -101.2000
    And the driver's player starts a track with the phone at 41.3000, -101.3000
    Then the route's track passes 41.2, -101.2 and then 41.3, -101.3
