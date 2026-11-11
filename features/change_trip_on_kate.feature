Feature: The active-trip route the trip switch posts to
  Kris, 2026-11-08: "We should be able to Change RoadTrip when we are
  developing like this. When you are on a road trip, you can only be on one
  at a time." One trip is active; the signed-in trip switch changes which.

  Background:
    Given a site with two trips, the second of them active
    And kris is signed in as an admin

  Scenario: Choosing a trip from the browser makes it active and returns to the driver screen
    Given the site is a KATE
    When kris chooses the first trip from the browser
    Then the browser is sent to the driver screen
    And the first trip is the active trip

  Scenario: A JSON caller still gets its JSON answer
    When a script posts the first trip as the active trip
    Then the answer is JSON naming the first trip as active
