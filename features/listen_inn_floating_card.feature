Feature: Listen Inn is its own floating card, off the map
  Kris, 2026-11-16: the Listen Inn used to sit on the map as a corner control.
  Now it is a card of its own that floats docked at the bottom of the Ride page,
  the way the driver's music card does: the title is its header button, it opens
  and closes in place, and there is one audio element. The two cards read as one
  family and wear the same music mark. Inside the card time flows downward: the
  oldest song is at the top and the card opens on the newest.

  Background:
    Given the trip is active and the driver is signed in with RoadTracks on

  Scenario: A visitor's Ride page carries Listen Inn as a docked card outside the map
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    Then the Ride page for a visitor carries the Listen Inn card docked after the page content
    And the card's title is its header button and the card starts closed
    And the card holds one audio element and one Play button

  Scenario: The map no longer takes the Listen Inn onto itself
    Then the map script adds no Listen Inn control to the map
    And the map still draws a music note for a play with a point

  Scenario: Both music cards wear the same mark and the same style
    Then the Listen Inn card and the driver's music card both show the music mark from this site's own files
    And the driver's music card no longer shows the helper's icon
    And one style rule dresses both cards
    And no page prints the mark's internal name

  Scenario: The list runs oldest to newest
    Given the song "First Song" played before the song "Second Song"
    Then the list shows "First Song" above "Second Song"

  Scenario: The card opens on the newest song unless a song was asked for
    Then with the songs "a1", "b2" and "c3" played in that order the card opens on "c3"
    And with the songs "a1", "b2" and "c3" played in that order and "b2" asked for the card opens on "b2"
    And with the songs "a1", "b2" and "c3" played in that order and "zz" asked for the card opens on "c3"

  Scenario: A song's time is shown in the zone where it played
    Then a song that started at "2026-11-13T23:23:00Z" in "America/Chicago" reads "5:23 PM" and never "UTC"

  Scenario: On a phone the open card leaves the map in sight
    Then at phone width the open song list is capped at 30vh or less
    And the page keeps room under its content for the docked card

  # Kris, 2026-11-18, on the road: "we now see both Drive Tracks and Listen Inn
  # at the same screen. No double player please." Inside the signed-in player
  # frame Drive Tracks is the one player card; the Listen Inn card is not drawn.
  Scenario: A signed-in driver's Ride page inside the player frame has no Listen Inn card
    Given the driver has left the start
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    Then the Ride page inside the driver's player frame carries no Listen Inn card
