Feature: Change the active trip from the KATE driver screen
  Kris, 2026-11-08: "We should be able to Change RoadTrip when we are
  developing like this. When you are on a road trip, you can only be on one
  at a time." One trip is active; the KATE (the test rung that allows
  simulated drives) offers a control to change which.

  Background:
    Given a site with two trips, the second of them active
    And kris is signed in as an admin

  Scenario: On a KATE the driver screen offers every trip with the active one selected
    Given the site is a KATE
    When kris looks at the driver screen for a trip control
    Then the trip control offers both trips
    And the active trip is the selected one

  Scenario: Off a KATE there is no trip control
    Given the site is not a KATE
    When kris looks at the driver screen for a trip control
    Then there is no trip control

  Scenario: Choosing a trip from the browser makes it active and returns to the driver screen
    Given the site is a KATE
    When kris chooses the first trip from the browser
    Then the browser is sent to the driver screen
    And the first trip is the active trip

  Scenario: A JSON caller still gets its JSON answer
    When a script posts the first trip as the active trip
    Then the answer is JSON naming the first trip as active
