Feature: The driver screen opens on one row of the jobs done at a stop
  Kris, 2026-11-18, from the road: at a stop the driver has one thumb and a
  glance. The five jobs (Navigate to the next stop, Check in here, Post a
  photo, Drop Pin, Ask Chester) lived in four places. They are now one row at
  the top of the driver screen, each one tap, none needing typing. Navigate
  opens the maps app the traveller chose, kept on the server and never in a
  browser. Check in is offered only within the radius of an open stop, and it
  goes to the same post form as the Ride page, so the causality rules still
  guard it. Every place and address here is invented.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at
    And the drive has started at A
    And Chester is on for the stop row

  Scenario: The driver screen opens on the five jobs in one row
    When kris reads the driver screen's stop row
    Then the stop row carries Navigate, Check in, Post, Drop Pin and Chester in that order
    And the stop row comes before the music card and the road section

  Scenario: Each job is a large button and needs one tap
    When kris reads the driver screen's stop row
    Then every stop row button is a link or a button with no field to fill
    And the stylesheet makes every stop row button at least 56 pixels tall

  Scenario: Navigate goes to the next stop's address in Waze by default
    Given B has the private address "12 Alder Lane"
    When kris reads the driver screen's stop row
    Then Navigate opens the Waze link for "12 Alder Lane"

  # Kris ruled 2026-11-26 (rt-spring-burn-2026-11-26, r2-retire-old-navigate):
  # the road section's own Navigate in Waze and its Google and Apple buttons
  # are retired, so every Navigate on the page is the stop row's, the one the
  # contrast guard covers.
  Scenario: The stop row holds the only Navigate on the driver screen
    Given B has the private address "12 Alder Lane"
    When kris reads the driver screen's stop row
    Then the driver screen has exactly one Navigate and it is the stop row's
    And the road section carries no Waze, Google Maps or Apple Maps button

  Scenario: Navigate goes to the maps app the traveller chose
    Given B has the private address "12 Alder Lane"
    When kris chooses Google Maps for Navigate
    And kris reads the driver screen's stop row
    Then Navigate opens the Google Maps link for "12 Alder Lane"
    And the server keeps the choice for kris

  Scenario: With no address on file Navigate is disabled and says so
    Given no stop is marked navigate by point
    When kris reads the driver screen's stop row
    Then Navigate is disabled and says "No address on file"

  Scenario: Check in is offered within the radius of an open stop
    When the stop row phone fix is at B
    Then the live screen offers Check in at B
    And the Check in link goes to the post form for B

  Scenario: Check in is not offered far from every stop
    When the stop row phone fix is 100 km from every stop
    Then the live screen offers no Check in

  Scenario: Before the phone has said where it is, Check in is disabled
    When kris reads the driver screen's stop row
    Then Check in is disabled and says "Not at a stop"

  Scenario: A rider who is not on the ride sees Drop Pin and Post only
    When a rider with no ride reads the driver screen's stop row
    Then the stop row carries only Post and Drop Pin

  Scenario: On a phone the row fits inside the gutter with no sideways scroll
    Given the stylesheet is read for the stop row
    Then the stop row is a grid that wraps to three across under 400 pixels and never overflows
