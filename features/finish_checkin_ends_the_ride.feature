Feature: Checking in at the route's end shows the ride as Finished
  Kris, 2026-11-10 KATE test drive: he checked in at the finish with a stop
  left open and the banner did not read finished. The data had the finish
  stamped; the banner read "Planning", the same word it shows before the drive
  starts, so a finished ride looked like one that never began. A ride is
  Planning before the Start, Active from the Start, and Finished once its
  finish is reached or skipped. Checking in at the finish with an earlier
  stop left open skips that stop, and the ride is Finished whether the finish
  is a loop back to the start or a different place.

  Background:
    Given a drive with a start, two stops and a finish
    And the driver Kris is signed in

  Scenario: A loop ride, a stop left open, Check in at the finish
    Given the finish lies at the start
    And the drive has started
    And the first stop is reached
    When Kris checks in at the finish
    Then the banner reads "Finished"
    And the second stop is skipped as missed

  Scenario: A non-loop ride, a stop left open, Check in at the finish
    Given the drive has started
    And the first stop is reached
    When Kris checks in at the finish
    Then the banner reads "Finished"
    And the second stop is skipped as missed

  Scenario: A ride still driving is not Finished
    Given the drive has started
    And the first stop is reached
    Then the banner reads "Active"

  Scenario: Before the Start the ride is Planning, not Finished
    Then the banner reads "Planning"
