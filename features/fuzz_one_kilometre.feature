Feature: A private place is fuzzed to one kilometre, not five
  Kris, 2026-11-14: the fuzzy location was enforced over about 5 km, which is
  too big. The grid is now 0.01 degrees (a cell about 1 km across) and the
  protected radius is 1 km: a point farther than that from a private place is
  published as itself, a nearer one is published as the place's snapped cell,
  and a route line is hidden only inside that 1 km. All points here are
  synthetic.

  Background:
    Given a private checkpoint whose real point is 38.4312, -112.3447

  Scenario: A fuzzed checkpoint is published on the 0.01 degree grid
    When the store hands the checkpoint out
    Then it is published at 38.43, -112.34

  Scenario: A pin 1.5 km from the private place is published at its own point
    When a pin 1.5 km north of the published place is published
    Then the pin is published at its own point, rounded to three decimals
    And the pin is not taken for the private place

  Scenario: A pin 0.8 km from the private place is published as the snapped cell
    When a pin 0.8 km north of the published place is published
    Then the pin is published at 38.43, -112.34
    And the pin is taken for the private place

  Scenario: A route line is hidden only inside 1 km of the private place
    Given a route line with vertices 1.5 km, 0.8 km and 0 km north of the published place
    When the line is masked
    Then the line keeps its 1.5 km vertex
    And the line has no vertex nearer than 1 km except the place's snapped point
