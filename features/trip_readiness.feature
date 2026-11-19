Feature: The trip-keeper checks where every Navigate link goes before leaving
  (Kris, 2026-11-18.) The readiness page lists each stop in order with where
  its Navigate links will go and the three links to open. The check is the
  trip-keeper opening the link: no geocoder is called. A "Checked" tick is kept
  on the server for each stop and clears when the stop's address changes. The
  page is the trip-keeper's alone. Every place and address here is invented.

  Background:
    Given the trip-keeper pushes with the push token and is signed in
    And a trip document whose stops carry no address and no navigate mark
    And the stop "Dogwood" is marked navigate by point
    And the trip is pushed
    And the overlay is pushed by ride path with addresses for "Birch Gate", "Cedar Camp" and "Elm"
    And the trip-keeper makes the trip active by name

  Scenario: The readiness page lists every stop with where Navigate will go
    When the trip-keeper opens the readiness page
    Then it lists the stops in order: "Alder", "Birch Gate", "Cedar Camp", "Dogwood", "Elm"
    And the row for "Birch Gate" says Navigate goes to its address and offers Waze, Google Maps and Apple Maps
    And the row for "Dogwood" says Navigate goes by point
    And the row for "Alder" says it is the start

  Scenario: A visitor cannot open it
    When a visitor opens the readiness page
    Then the visitor is not given the page
    And the visitor's answer carries no stop name

  Scenario: A tick is kept on the server and clears when the stop's address changes
    When the trip-keeper ticks "Birch Gate" as checked
    And the trip-keeper opens the readiness page
    Then the row for "Birch Gate" is checked
    And the row for "Cedar Camp" is not checked
    When the overlay is pushed by ride path with the entry 2 for "Birch Gate" at "11 Heron Court, Pemberwick"
    And the trip-keeper opens the readiness page
    Then the row for "Birch Gate" is not checked
