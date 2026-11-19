Feature: The Listen Inn sits in the page above the map and never over it
  Kris, 2026-11-18: "move the Listen Inn off the map as the map should not be
  obscured if possible. Put it in the white space above." On the Ride page the
  Listen Inn card is part of the page, in the band at the top beside the latest
  tiles, so nothing is ever drawn over the map, at phone width too. It replaces
  the Last played tile (ruling 8, the same day): the card's own line says what
  is playing, so the page does not print it twice. Inside the signed-in player
  frame there is still no Listen Inn card unless it was asked for (PR 112).

  Background:
    Given the trip is active and the driver is signed in with RoadTracks on
    And the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started

  Scenario: On the Ride page the Listen Inn card comes before the map in the page
    Then the Ride page carries the Listen Inn card before the map, in the top band

  Scenario: No style rule fixes the Listen Inn card to the window
    Then no style rule fixes the Listen Inn card or floats it over the page

  Scenario: The page reserves no empty space at its foot for a docked card
    Then the page reserves no padding at its foot for a docked card

  Scenario: From tablet width the card shares a row with the latest tiles and is no wider than 24 rem
    Then from 768 px the top band is one row and the card is no wider than 24rem

  Scenario: On a phone the card is full width inside the 16 px gutter and the page does not scroll sideways
    Then under 768 px the top band stacks and the card is full width with a 16px gutter and no sideways scroll

  Scenario: Closed, the card is its title and one line
    Then the closed card holds its title button and one line and is at most 72px tall

  Scenario: Open, the list scrolls inside itself and the map is pushed down, not covered
    Then the open list scrolls inside itself and takes its own room in the page

  # Kris, 2026-11-18, ruling 8: the Listen Inn card REPLACES the "Last played" tile.
  Scenario: With the card on the page the Last played tile is not drawn, and without it the tile is
    Then the Ride page for a visitor draws no song tile in the latest strip
    And the Ride page inside the driver's player frame still draws the song tile

  Scenario: A visitor still gets one audio element and one Play button
    Then the Ride page for a visitor holds one audio element and one Play button

  Scenario: A song link opens the same in-page card at the top, open
    Then the song link of "Come Together" renders the card in the top band, open
