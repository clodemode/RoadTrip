Feature: A one-day trip's badge reads Today
  Kris ruled, 2026-11-08: "Day 1 of 1" tells a driver nothing. A trip with one
  day reads Today, on the home page badge and the driver screen's day gauge.
  A longer trip keeps its count.

  Scenario: A one-day trip reads Today
    Given a trip of 1 day, on its day 1
    Then the day badge reads "Today"

  Scenario: A longer trip keeps its count
    Given a trip of 4 days, on its day 2
    Then the day badge reads "Day 2 of 4"

  Scenario: The driver screen's gauge follows the same rule
    Then the driver screen's day gauge says Today on a one-day trip, as drawn and as refreshed

  Scenario: A trip past its last day that has not reached the finish is still driving
    # Kris on the KATE, 2026-11-09: "It says right now I have Arrived. I only
    # posted a couple of photos."
    Given a trip whose last day was yesterday, with its first stop reached and its finish not
    Then the day badge reads "Still driving"

  Scenario: A trip that reached its finish has arrived
    Given a trip whose last day was yesterday, with its finish reached
    Then the day badge reads "Arrived"
