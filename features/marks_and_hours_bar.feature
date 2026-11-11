Feature: Letters for places, numbers only for driving days, and an hours bar
  Letters name places. A number always carries its unit as a prefix: d2 is
  driving day 2, h2 is hour 2. A one-day trip has no days to number, so every
  point on it is a letter, in route order, start included. A trip of several
  days marks its day ends d1, d2, d3, and its day chips the same.
  The map shows each day's estimated time as small hour labels beside the
  route: it starts at the day's origin at h0, and every whole hour after that
  is labelled with the place the car should be at by then, at the trip's
  average speed along the planned line. (The Plan page no longer draws a row of
  hour dots for each day; see plan_day_summaries.feature.)

  Background:
    Given kris is signed in

  Scenario: A one-day trip marks every point with a letter
    Given the active trip is one day with stops Kingston, Napanee, Belleville, Trenton, Cobourg, Oshawa and Toronto
    When anyone opens the home page
    Then the stops are marked A, B, C, D, E, F and G in that order
    And no stop is marked with a number
    And the map data carries the same marks

  Scenario: A one-day trip shows no day number
    Given the active trip is one day with stops Kingston, Napanee, Belleville, Trenton, Cobourg, Oshawa and Toronto
    When anyone opens the home page
    Then no day chip shows a number

  Scenario: A trip of several days keeps numbers on its day ends
    Given the active trip is two days
    When anyone opens the home page
    Then each day end is marked "d" and its day number, like "d1" and "d2"
    And every other stop is marked with a letter
    And each day chip reads "d" and its day number

  Scenario: The hours start at the origin at 0
    Given the active trip is one day from Kingston to Toronto at 60 mph over 165 miles
    When anyone opens the home page
    Then the hours bar reads "h0 Kingston" first

  Scenario: The map draws the hours bar as a faint stick beside the route
    Given the active trip is one day from Kingston to Toronto at 60 mph over 165 miles
    When anyone opens the home page
    Then the map data carries an hours line for the leg with ticks h0, h1 and h2
    And each tick lies on the planned line at that whole hour's distance
    And the hours line is styled muted and drawn offset beside the route, not on it

  Scenario: Each whole hour names the place the car should be by then
    Given the active trip is one day from Kingston to Toronto at 60 mph over 165 miles
    And a known place sits on the planned line 60 miles from Kingston
    When anyone opens the home page
    Then the hours bar marks "h1" with that place's name

  Scenario: An hour with no known place nearby shows the hour alone
    Given the active trip is one day whose line has no known place near its 1 hour point
    When anyone opens the home page
    Then the hours bar marks "h1" with no name

  Scenario: The hours bar never names a private place
    Given the active trip is one day with a private stop near its 1 hour point
    When anyone opens the home page
    Then the hours bar marks "h1" without the private stop's name

  Scenario: A check-in re-measures the hours bar from where the car got to
    Given the active trip is one day from Kingston to Toronto at 60 mph over 165 miles
    And the stop 60 miles from Kingston is arrived at
    When anyone opens the home page
    Then the hours bar reads "h0" at that stop first
    And its h1 tick lies 60 miles further along the planned line

  Scenario: A skipped stop does not move the hours bar
    Given the active trip is one day from Kingston to Toronto at 60 mph over 165 miles
    And the stop 60 miles from Kingston is skipped
    When anyone opens the home page
    Then the hours bar reads "h0 Kingston" first
