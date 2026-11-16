Feature: The stacked test setup keeps every scenario alone
  The test binary creates the schema, runs the migrations and seeds the ride
  once, and hands every site its own copy of that store. Building the store
  once must never let one scenario see what another wrote.

  Scenario: A scenario changes its own store
    Given a site built from the stacked setup
    When every checkpoint in its store is renamed "Renamed by the scenario before"
    Then its store holds a checkpoint named "Renamed by the scenario before"

  Scenario: The neighbour does not see the change
    Given a site built from the stacked setup
    Then its store holds no checkpoint named "Renamed by the scenario before"
    And its store holds the same checkpoints as a store seeded from nothing

  Scenario: Two sites in one scenario do not share a store
    Given a site built from the stacked setup
    And a second site built from the stacked setup
    When every checkpoint in its store is renamed "Renamed in the first site"
    Then the second site's store holds no checkpoint named "Renamed in the first site"
