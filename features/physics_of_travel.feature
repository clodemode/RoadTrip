Feature: Think like a human, a car can only go so far in the time
  A check-in is a claim about where the car is. The app does not believe a
  claim the car could not have made: a position too far from the last
  check-in for the time between them does not check in, and its post is kept
  and flagged as out of step. A photo older than the last check-in is posted
  at its own time and does not move the car back. A time in the future is not
  believed: the phone's time is used and the post says so. An arrival
  estimate is never earlier than the miles left allow at the road limit.
  The limit is generous (250 km/h), the slack is the check-in radius plus the
  position's own accuracy, and a short gap is judged as at least two minutes,
  so a poor fix or a long silence is never called out of step. Nothing here
  stops a post from saving.
  The stops are invented, 55 km apart along a line, and the clock is the
  synthetic 7 November.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at

  Scenario: A position the car could not have reached does not check in
    Given the car was last checked in at A at 10:00
    And a photo taken at 10:05 with its GPS at D
    When kris posts that photo
    Then the post exists
    And no stop is checked in
    And the post is flagged as out of step
    And the notice says the post was kept as out of step

  Scenario: A fast but possible drive still checks in
    Given the car was last checked in at A at 10:00
    And a photo taken at 10:20 with its GPS at B
    When kris posts that photo
    Then B is arrived at 10:20 in B's zone
    And the post is not flagged

  Scenario: A phone fix with poor accuracy gets the benefit of the doubt
    Given the car was last checked in at A at 10:00
    And a photo with no GPS of its own
    And the phone reports a position at B with an accuracy of 20000 metres
    When kris posts that photo
    Then B is arrived at
    And B is arrived at the phone's time
    And the post is not flagged

  Scenario: The same fix with no accuracy is out of step
    Given the car was last checked in at A at 10:00
    And a photo taken at 10:10 with its GPS at B
    When kris posts that photo
    Then the post exists
    And no stop is checked in
    And the post is flagged as out of step

  Scenario: A long silence is never out of step
    Given the car was last checked in at A at 06:00
    And a photo taken at 23:00 with its GPS at D
    When kris posts that photo
    Then D is arrived at 23:00 in D's zone
    And the post is not flagged

  Scenario: A photo older than the last check-in does not move the car back
    Given the car was last checked in at B at 12:00
    And a photo taken at 11:30 with its GPS at C
    When kris posts that photo
    Then the post exists at 11:30 in its own zone
    And no stop is checked in
    And the post is not flagged
    And B is still arrived at 12:00 in B's zone

  Scenario: A time in the future is not believed
    Given the car was last checked in at A at 10:00
    And a photo taken in the year 2099 with its GPS at B
    When kris posts that photo
    Then the post exists at the phone's time
    And the post says its time came from the phone
    And B is arrived at the phone's time

  Scenario: A position with no place checks in nothing, and saves
    Given the car was last checked in at A at 10:00
    And a photo with no GPS of its own
    When kris posts that photo
    Then the post exists
    And no stop is checked in

  Scenario: The arrival estimate is never earlier than the miles left allow
    Given a leg of 165 km whose plan says it takes six minutes
    When the HUD is asked moving at its start
    Then the ETA written to the ledger is at least 39 minutes ahead
