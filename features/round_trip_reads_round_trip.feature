Feature: A trip that ends where it starts reads Round trip, and a one-way trip still reads One-way
  Kris, 2026-11-18: a lot of trips are round trips, not one-way. The ride page
  printed a One-way pill on every trip, the page description said a one-way
  drive, and the plan data said ONE-WAY. Whether a trip is a round trip is one
  fact of the ride, worked out in one place from where it starts and where it
  finishes (the same place, or two stops within half a kilometre), and every
  surface reads that one fact. The stops used here are invented.

  Background:
    Given kris is signed in

  Scenario: A trip that ends where it starts says Round trip and names the places between
    Given the active trip is a round trip: A, B and C in order, then the finish D back at A
    When a visitor reads the ride page
    Then the hero line reads "Alder, round trip by way of Birch Gate and Cedar Camp"
    And the hero carries the "Round trip" pill

  Scenario: A one-way trip still says One-way
    Given the active trip has stops A, B, C and D in order along a line, none arrived at
    When a visitor reads the ride page
    Then the hero line reads "Alder → Dover Dock"
    And the hero carries the "One-way" pill

  Scenario: The page description, the plan page and the plan data agree with the hero
    Given the active trip is a round trip: A, B and C in order, then the finish D back at A
    When a visitor reads the ride page
    Then the sign-in page description says "a round trip"
    And the plan page says "Round trip"
    And the plan data says it is a round trip

  Scenario: The trip picker says Round trip beside the trip's name
    Given the active trip is a round trip: A, B and C in order, then the finish D back at A
    When a visitor reads the ride page
    Then kris's trip picker labels the active trip "Round trip"

  Scenario: The plan page and the plan data of a one-way trip say One-way
    Given the active trip has stops A, B, C and D in order along a line, none arrived at
    When a visitor reads the ride page
    Then the sign-in page description says "a one-way drive"
    And the plan page says "One-way"
    And the plan data says it is not a round trip
