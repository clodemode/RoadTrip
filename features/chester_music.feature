Feature: Chester finds music in the library and shows it as a Play card
  Kris, 2026-11-08: Chester can search the MusicOrgan library and open music
  from it. The album Chester recommends is a card with a bold, large Play
  button. There is no mini-player and no audio through RoadTrip. Play is a link
  that opens the album page on the library in a new tab, and music does not
  start by itself (no autoplay parameter), so it is two taps.
  The model is never given the library's address or the shared key: it names an
  album by a short reference from this question's own search, and the server
  builds the card and its link. A reference the search did not return is
  dropped, so Chester cannot invent an album or a link.

  Background:
    Given the music library holds "Abbey Road" by "The Beatles" from 1969

  Scenario: A music search returns a card with a Play link to the album
    Given RoadTracks is on
    And Chester will search the library for "abbey road"
    And Chester will then show the album "m1" because "A calm, familiar one for the long stretch"
    And Chester will then write "Abbey Road suits an open highway. Tap Play to open it."
    When the trip-keeper asks Chester "Something to drive to, like the Beatles"
    Then the answer carries one album card for "Abbey Road" by "The Beatles"
    And the card says "A calm, familiar one for the long stretch"
    And the card's Play link opens the album on the library
    And the card's link carries no autoplay parameter
    And the answer was written after the last tool call
    And the page builds Play as a new-tab link, at least 56 pixels tall and the width of the card

  Scenario: An album that the search did not return gets no card
    Given RoadTracks is on
    And Chester will search the library for "abbey road"
    And Chester will then show the album "m9" because "Invented, never in the results"
    And Chester will then write "I could not find that one in the library."
    When the trip-keeper asks Chester "Play me something by Nobody"
    Then the answer carries no album card

  Scenario: With RoadTracks off Chester is offered no music tools
    Given RoadTracks is off
    And Chester will write "Fuel is ahead at Birch Gate."
    When the trip-keeper asks Chester "Where is the next fuel?"
    Then Chester was offered no music tool
    And the system prompt says nothing about the music library
    And the answer carries no album card

  Scenario: A library failure is told honestly and leaks neither key nor address
    Given RoadTracks is on
    And the music library is down
    And Chester will search the library for "abbey road"
    And Chester will then write "The music library is unavailable right now, so I cannot suggest an album."
    When the trip-keeper asks Chester "Something to drive to"
    Then the answer carries no album card
    And Chester was told the music library is unavailable
    And nothing sent to the model or to the page holds the library key or address
    And the answer says the music library is unavailable

  Scenario: The model is never sent a URL or the key
    Given RoadTracks is on
    And Chester will search the library for "abbey road"
    And Chester will then show the album "m1" because "Familiar"
    And Chester will then write "Tap Play to open it."
    When the trip-keeper asks Chester "Something to drive to"
    Then Chester was told the album "Abbey Road" by "The Beatles" and its reference "m1"
    And nothing sent to the model holds the library key or address

  Scenario: A music search is logged on the bus by length and count, never the words
    Given RoadTracks is on
    And Chester will search the library for "abbey road"
    And Chester will then write "Nothing to show."
    When the trip-keeper asks Chester "Something to drive to"
    Then the bus carries a chester_music_search record with the query length 10 and the count 1 and not the query
