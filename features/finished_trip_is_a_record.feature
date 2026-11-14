Feature: A finished trip is a record, and it spends nothing
  Kris, 2026-11-14, after the finish at Hudson Bend: the ended trip's site stays
  up for visitors, with its map, posts, photos and Listen Inn history, but the
  live-drive parts go: the drive HUD, the now-playing chip, the ETA and the
  Waze and Navigate links. Chester is off while no trip is running, so a ride
  whose finish is reached or skipped makes no API call at all; he comes back
  when a new, unfinished ride becomes active. "Finished" is the same state the
  banner reads (rideFinished): nothing new is stored.

  Background:
    Given a ride with a start, two stops and a finish, Chester on, music played and a post made
    And Kris is signed in as the trip-keeper

  Scenario: The home page of a finished ride is a record
    Given the ride is finished
    When a visitor reads the home page
    Then the page shows the map and the post
    And the page shows the Listen Inn history
    But the page shows no now-playing chip, no drive HUD, no ETA and no Waze link
    When Kris reads the home page
    Then the page shows no now-playing chip, no drive HUD, no ETA and no Waze link

  Scenario: A visitor can still open the Listen Inn history of a finished ride
    Given the ride is finished
    Then a visitor reads the tracks document with the played track in it

  Scenario: The driver screen of a finished ride has no ETA and no navigation
    Given the ride is finished
    When Kris reads the driver screen
    Then the driver screen hides the ETA and the navigation links

  Scenario: Chester refuses on a finished ride and the API is never called
    Given the ride is finished
    When Kris asks Chester a question
    Then Chester answers with the sentence "Chester is resting until the next trip starts."
    And the API stub recorded 0 calls

  Scenario: Chester still answers on a ride that is not finished
    Given the ride's drive has started
    When Kris asks Chester a question
    Then Chester gives an answer
    And the API stub recorded 1 calls
