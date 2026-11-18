Feature: A round trip's finish back at its start is the next stop once the last stop is done
  Kris, 2026-11-18, after the Manor inspection and lunch in Del Valle: the
  driver screen said there was no stop ahead and gave no Waze link, when it
  should have routed home to Hudson Bend. The finish sits on the start, so
  measuring where it lies along the route found the route's first mile and
  counted the finish as already behind the car. A stop is now measured along
  the route from the stop before it, so a finish at the start lands at the
  route's end.

  Background:
    Given kris is signed in
    And the active trip is a round trip: A, B and C in order, then the finish D back at A
    And the drive has started at A

  Scenario: With B and C arrived at, the next stop is the finish and it offers navigation
    Given the round trip's B and C are arrived at
    When the driver screen is asked with no position
    Then the next stop on the driver screen is D, with Waze, Google and Apple links

  Scenario: With only B arrived at, the next stop is C, not the finish
    Given the round trip's B is arrived at
    When the driver screen is asked with no position
    Then the next stop on the driver screen is C, with Waze, Google and Apple links
