Feature: An arrival is stamped by what vouched for it and never runs backward
  Kris, 2026-11-08, on the KATE: a drive departed at 9:38 PM, a library photo
  taken at 15:00 stepped the simulated car to a stop and the stop read
  "Arrived 3:00 PM", hours before departure. An arrival is a claim about when
  the car got there, and only the photo's own GPS puts the car where the photo
  was taken, so only a photo-vouched arrival takes the photo's clock. A phone
  vouches at the moment it speaks, and the simulated car arrives now. No
  arrival is ever earlier than the latest stamp on the ride, the start
  included: such an arrival is not checked in, the post is kept at its own time
  and the notice says so. The simulated car is no longer exempt from that
  floor. Nothing here stops a post from saving.
  The stops are invented, 55 km apart along a line, and the photo clocks are
  the synthetic 7 November.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at

  Scenario: A phone-vouched arrival is stamped at the phone's time, not the photo's
    A photo with no GPS that is more than 20 minutes old never checks in at
    all (old_photo_without_place.feature); this one is a fresh camera shot.
    Given the drive has started at A
    And a photo taken 10 minutes ago with no GPS of its own
    And the phone reports a position at B
    When kris posts that photo
    Then B is arrived now

  Scenario: A simulated-car arrival is stamped now
    Given simulation is allowed
    And the drive has started at A
    And kris's simulated car is at B
    And a photo taken at 15:00 with its GPS 900 km from every stop
    When kris posts that photo
    Then B is arrived now
    And the post exists at 15:00 in its own zone

  Scenario: A photo-vouched arrival takes the photo's clock
    Given the drive has started at A
    And a photo taken at 10:20 with its GPS at B
    When kris posts that photo
    Then B is arrived at 10:20 in B's zone

  Scenario: An arrival earlier than the start does not check in
    Given the drive started at A two hours from now
    And a photo taken at 15:00 with its GPS at B
    When kris posts that photo
    Then B is still not arrived at
    And the post exists
    And the notice says the post was kept at its own time and the car stayed

  Scenario: An arrival earlier than the latest check-in does not check in
    Given the drive has started at A
    And B is arrived two hours from now
    And a photo taken at 15:00 with its GPS at C
    When kris posts that photo
    Then C is still not arrived at
    And the post exists
    And the notice says the post was kept at its own time and the car stayed

  Scenario: The simulated car obeys the floor
    Given simulation is allowed
    And the drive started at A two hours from now
    And kris's simulated car is at B
    And a photo taken at 10:42 with its GPS 900 km from every stop
    When kris posts that photo
    Then B is still not arrived at
    And the post exists
    And the notice says the post was kept at its own time and the car stayed
