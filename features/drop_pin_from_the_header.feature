Feature: One Drop Pin button in the header drops a dot on the map
  Kris, 2026-11-18, on the road: "Provide a single button drop point on all
  screens, in header, for driver or rider." Ruled the same morning: a pin is a
  dot on the map only, never a post on the timeline. One tap stores where the
  phone is, as a published point (a private place is its snapped cell), on the
  active ride. A pin never checks in at a stop and never skips one: that
  morning a post from home checked in at the round trip's finish and ended the
  trip. The button is for anyone signed in; a visitor has none.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at
    And the drive has started at A

  Scenario Outline: The header carries one Drop Pin button on every page for a signed-in user
    When kris opens the page "<page>"
    Then the header carries exactly one Drop Pin button

    Examples:
      | page    |
      | /       |
      | /driver |
      | /plan   |
      | /photos |

  Scenario: A visitor's header has no Drop Pin button
    When a visitor opens the Ride page for its header
    Then the header carries no Drop Pin button

  Scenario: A pin dropped at the last stop is a dot on the map and checks in nowhere
    When kris drops a pin with the phone at D
    Then the pin is accepted
    And no stop after A is arrived at or skipped
    And the pin made no post
    And the Ride map carries one pin

  Scenario: A visitor cannot drop a pin
    When a visitor drops a pin with the phone at D
    Then the pin is refused
    And the Ride map carries no pin
