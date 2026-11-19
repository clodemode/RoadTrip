Feature: A round trip's finish is reached only after the car has been away, and no check-in skips the whole trip
  Kris, 2026-11-18: a photo posted from home after the drive began checked in
  at the finish (it shares the start's point), stamped the two stops between as
  skipped, and put Chester to rest. Ruled the same day: a finish at the start
  is reached only after the car has been away, and a check-in that would skip
  every remaining stop ASKS first, inline on the page, and is never refused
  outright. A person's own tap on the finish stays valid.

  Background:
    Given kris is signed in
    And the active trip is a round trip: A, B and C in order, then the finish D back at A
    And the drive has started at A

  Scenario: A post from the start after the drive starts checks in nowhere and skips nothing
    Given a photo taken at 13:00 with its GPS at D
    When kris posts that photo
    Then the notice says "Posted; still at the start"
    And the round trip's B, C and D are untouched

  Scenario: After a stop is arrived, a post at the start reaches the finish
    Given the round trip's B and C were reached on the road
    And a photo taken at 13:00 with its GPS at D
    When kris posts that photo
    Then the round trip's finish D is arrived at

  Scenario: After a position away from the start, a post at the start that would skip every stop asks and stamps nothing
    Given the car was seen 30 km from the start
    And a photo taken at 13:00 with its GPS at D
    When kris posts that photo
    Then the notice says "Near the finish with 2 stops not reached. Check in at the finish by hand if the trip is over"
    And the round trip's B, C and D are untouched
    And the page carries the hand check-in for the finish

  Scenario: A hand check-in at the finish still ends the trip
    When kris checks in at the round trip's finish by hand
    Then the round trip is finished
