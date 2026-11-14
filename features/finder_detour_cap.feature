Feature: The finder keeps detours to about five minutes each way
  Kris, 2026-11-14: "no more than 5 minutes each way" off the route. TomTom's
  detourTime is the whole added time, there and back, so the cap is 600
  seconds. The cap is sent to TomTom and also enforced here, so a place that
  comes back over it is never offered even if the server ignored the
  parameter. TomTom is a local stub; nothing here reaches the real service.

  Background:
    Given a driver heading to "hudson bend" at "2026-11-07T15:15:00-06:00"

  Scenario: A place 480 seconds off the route is offered
    Given TomTom finds "Quick Detour Diner" for "barbecue" with a detour of 480 seconds
    When the driver taps Find picks
    Then the food picks include "Quick Detour Diner"
    And TomTom was asked with a detour cap of 600 seconds

  Scenario: A place 900 seconds off the route is not offered
    Given TomTom finds "Far Away Diner" for "barbecue" with a detour of 900 seconds
    When the driver taps Find picks
    Then the food picks do not include "Far Away Diner"
    And the food category says no place was found within about 5 minutes of the route
