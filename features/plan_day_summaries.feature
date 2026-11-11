Feature: Plan summarises each day in words, and the map marks each night
  Kris, 2026-11-11, on the Plan page: "my day timelines don't really have much
  value and the text is overlapping on the right". Each day is now one block:
  its badge (d1, d2 ...), the date, where it goes from and to, its miles and
  driving time, a plain "Long day" mark when the plan flags one, and the
  milestones passed that day with the hour each comes at. A stop is a place to
  stay the night. A milestone is a place passed on the way that marks progress.
  The first place of the plan is where it starts, and a private place is never
  named. Every place here is a real public town centre or an invented name.

  Scenario: A day is one block with its miles, its time and its milestones
    Given the active trip is one day from Kingston to Toronto at 60 mph over 165 miles
    And a stop "Maple Halt" lies 60 miles along the plan
    When anyone opens the Plan page
    Then day 1 reads "d1", "Day 1", "about 2¾ h" and "mi"
    And day 1 names the milestones "Maple Halt at about 1 h"
    And the Plan page draws no row of hour dots

  Scenario: A trip of several days counts its nights and its milestones
    Given the active trip is two days
    When anyone opens the Plan page
    Then the trip line reads "2 days"
    And the trip line reads "1 night"
    And the trip line reads "3 milestones"
    And day 1 names the milestones "Napanee at about"

  Scenario: A private place is never a milestone
    Given the active trip is one day with a private stop near its 1 hour point
    When anyone opens the Plan page
    Then day 1 names no milestones

  Scenario: The map marks each night's end with its day badge and the rest as small dots
    Then the Plan map and the Ride map draw a night's end as its day badge with the town
    And the Plan map draws the start and the milestones as small plain dots
