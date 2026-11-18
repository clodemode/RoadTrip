Feature: The departure is stamped when the car leaves the start, not when the trip is opened
  Kris, 2026-11-18: a packing photo in the driveway stamped the departure with
  the photo's clock, hours before the car moved. Ruled the same day: the
  departure stamps AUTOMATICALLY when the car is a position away from the start,
  and a "Driving now" button stamps it for a phone that sends no position. A
  Start post (the packing photo) opens the trip and stamps nothing.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at

  Scenario: A Start post with a photo opens the trip and leaves the departure unstamped
    Given a photo taken at 09:15 with its GPS at A
    When kris starts the drive with that photo
    Then the trip is open and A is not departed

  Scenario: The first position more than half a kilometre from the start stamps the departure at its own time
    Given the trip was opened by a Start post
    And a photo taken at 09:40 with its GPS 5 km north of A
    When kris posts that photo
    Then A is departed at 09:40 in A's zone

  Scenario: A position within half a kilometre of the start is still the start
    Given the trip was opened by a Start post
    And a photo taken at 09:40 with its GPS 0.2 km north of A
    When kris posts that photo
    Then the trip is open and A is not departed

  Scenario: A dropped pin away from the start stamps the departure
    Given the trip was opened by a Start post
    When kris drops a pin 5 km north of A
    Then A is departed

  Scenario: Until the car leaves, the driver screen says packing and offers Driving now
    Given the trip was opened by a Start post
    When kris looks at the driver screen
    Then the driver screen reads "Packing, not driving yet" and offers "Driving now"

  Scenario: Driving now stamps the departure for a phone that sends no position
    Given the trip was opened by a Start post
    When kris taps Driving now
    Then A is departed

  Scenario: Drive time counts from the departure, not from the Start post
    Given the trip was opened by a Start post
    Then no stop carries an arrival to measure drive time from
    When kris taps Driving now
    Then the drive time is measured from A's departure
