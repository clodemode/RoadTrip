Feature: The Ride page picks a past, current or next trip; visitors see current and past only
  Kris, 2026-11-16: "Our Ride/Drive pages need to let us select previous or
  next roadtrips, or current. Guests only see current and past as select
  options." Looking at a trip on the Ride page never changes the trip the
  site shows; the trip-keeper's switch on the Driver page does.

  Background:
    Given the ride picker has a past trip "Kingston Run", the current trip "Hill Loop" and a next trip "Waco Day"

  Scenario: A visitor is offered the current and past trips, never the next one
    When a ride picker visitor opens "/"
    Then the ride picker offers "Kingston Run"
    And the ride picker offers "Hill Loop"
    And the ride picker does not mention "Waco Day"

  Scenario: A visitor reads a past trip without changing the site
    When a ride picker visitor opens "/?ride=kingston-run"
    Then the ride picker page says it is a past trip named "Kingston Run"
    And the ride picker page links its legs on "kingston-run"
    And the site still shows "Hill Loop" to a ride picker visitor

  Scenario: A visitor who asks for the next trip by name is shown the current trip
    When a ride picker visitor opens "/?ride=waco-day"
    Then the ride picker does not mention "Waco Day"
    And the ride picker page is not looking at another trip

  Scenario: The trip-keeper is offered past, current and next, and reads the next trip without its tools
    When the ride picker keeper opens "/"
    Then the ride picker offers "Waco Day"
    When the ride picker keeper opens "/?ride=waco-day"
    Then the ride picker page says it is the next trip named "Waco Day"
    And the ride picker page carries no driver tools

  Scenario: The trip-keeper switches the trip the site shows from the Driver page
    When the ride picker keeper opens "/driver?embed=1"
    Then the ride picker switch lists "Kingston Run", "Hill Loop" and "Waco Day"
    When a ride picker visitor opens "/driver"
    Then the ride picker page has no keeper switch
