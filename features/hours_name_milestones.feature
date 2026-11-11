Feature: The hours cards name where you will be
  Each whole hour label the map draws beside the route names the nearest stop or routing point
  that lies near that distance along the plan, so "h2 Belleville" says where
  the car should be two hours out. A routing point is a place to drive
  through, not a stop, and it names an hour the same way a stop does. A
  private place is never named, and neither is anything that sits in its zone.
  Every place here is a real public town centre or an invented name on a
  synthetic trip.

  Scenario: An hour names a routing point near it
    Given the active trip is one day from Kingston to Toronto at 60 mph over 165 miles
    And a routing point "Pinecrest Corner" lies 60 miles along the plan
    When anyone opens the home page
    Then the hours bar marks "h1" as "Pinecrest Corner"

  Scenario: The nearer of a stop and a routing point names the hour
    Given the active trip is one day from Kingston to Toronto at 60 mph over 165 miles
    And a stop "Maple Halt" lies 63 miles along the plan
    And a routing point "Pinecrest Corner" lies 60 miles along the plan
    When anyone opens the home page
    Then the hours bar marks "h1" as "Pinecrest Corner"

  Scenario: A stop nearer than a routing point names the hour
    Given the active trip is one day from Kingston to Toronto at 60 mph over 165 miles
    And a stop "Maple Halt" lies 60 miles along the plan
    And a routing point "Pinecrest Corner" lies 63 miles along the plan
    When anyone opens the home page
    Then the hours bar marks "h1" as "Maple Halt"

  Scenario: A routing point inside a private place's zone is never named
    Given the active trip is one day with a private stop near its 1 hour point
    And a routing point "Quiet Lane" is stored beside that private stop
    When anyone opens the home page
    Then a private place is never named

  Scenario: A private stop alone is never named
    Given the active trip is one day with a private stop near its 1 hour point
    When anyone opens the home page
    Then a private place is never named
