Feature: A cancelled Chester question costs what the API reported, and the daily cap runs on the driver's day
  Production, 2026-11-13: two Chester questions cancelled within 37 seconds
  were charged their worst case (44 cents of the $5 day) for answers nobody
  got, and the cap's day was the UTC day, which ended at 18:00 on a Central
  afternoon drive. A question the driver cancels is now charged only what the
  API had already reported, nothing when it had reported nothing, and the log
  row says so. The cap's day is midnight to midnight in the zone the ride is
  in. The Messages API is stubbed; nothing here reaches Anthropic.
  Production rows already charged are not changed by this.

  Scenario: A question cancelled after the API reported its input is charged that input only
    Given the stubbed API reports 70 input tokens and then waits
    When the driver cancels the question while it waits
    Then the row is charged for the 70 input tokens and no written tokens
    And the row is not marked as an estimate
    And the row's note says only what the API reported was charged

  Scenario: A question cancelled before the API reported anything costs nothing
    Given the stubbed API says nothing and waits
    When the driver cancels the question while it waits
    Then the row is charged nothing
    And the row's note says only what the API reported was charged

  Scenario: A question stopped by the time limit is still charged its worst case
    Given the stubbed API reports 70 input tokens and then waits
    When the question runs out of time
    Then the row is charged more than its input tokens

  Scenario: The cap's day is the driver's day, not the UTC day
    Given the day's spending is read at 6:30 PM Central on 13 November
    And $2.00 was spent at 5:50 PM Central that day
    And $1.00 was spent at 9:00 PM Central the evening before
    Then the day's spending is $2.00
