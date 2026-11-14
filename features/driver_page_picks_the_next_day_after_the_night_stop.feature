Feature: The driver page reads the next day once the night stop is done
  The phone's position picks the day the Driver page reads: the day whose
  line the car is nearest. A hotel sits at the end of yesterday's line, so
  the morning after, the car is still nearest yesterday's line, every stop of
  yesterday is settled, and the page used to read yesterday with no next stop
  and no Navigate links. When the day the car is nearest has its last stop
  arrived or skipped and a later day exists, the page reads the later day and
  measures from its start. The date alone never decides it: a late arrival
  after midnight still reads the day being driven.

  Background:
    Given kris is signed in
    And the active trip is two days

  Scenario: The morning after, the car a few miles back along yesterday's line reads the next day
    Given every stop of day 1 is arrived at
    When the Driver page is asked with a position a few miles back along day 1's line from its last stop
    Then the Driver page reads day 2
    And the next stop is the first stop of day 2 and offers navigation

  Scenario: While yesterday's last stop is not arrived at, the page stays on yesterday
    Given the first two stops of day 1 are arrived at
    When the Driver page is asked with a position a few miles back along day 1's line from its last stop
    Then the Driver page reads day 1
    And the next stop is the last stop of day 1 and offers navigation

  Scenario: A skipped last stop settles the day too
    Given the first two stops of day 1 are arrived at and the last is skipped
    When the Driver page is asked with a position a few miles back along day 1's line from its last stop
    Then the Driver page reads day 2
    And the next stop is the first stop of day 2 and offers navigation
