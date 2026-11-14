Feature: Every Drive Tracks tap ends in one album card, never in words alone
  Kris, 2026-11-14, on the road: three of four taps on "Ask Chester for an
  album" ended with words and no album, because nothing told the model it had
  to use the music tool. A tap now forces the first request to search the
  library, and a turn that would end with no card gets one more request that
  forces show_music. Only Drive Tracks is forced: a question the traveller
  types is still the model's to answer as it likes. A model that is forced to
  call a tool is not asked to think first, because the API refuses both
  together. No test calls the real API; the stubbed model obeys the tool it is
  told to call and otherwise answers in words, which is how the live model
  behaved when it was not told.

  Background:
    Given the music library holds "Abbey Road" by "The Beatles" as album 42, playable
    And the trip-keeper is signed in with Chester and RoadTracks on

  Scenario: A model that would answer in words is told to search, and an album comes back
    Given the model obeys the tool it is told to call and otherwise answers in words
    When the driver taps Drive Tracks
    Then the first request to the model forced the tool "search_music"
    And the first request to the model did not ask for adaptive thinking
    And exactly one album card came back, for album 42
    And the card is told to start album 42 "Abbey Road" by "The Beatles"

  Scenario: A model that searches and then ends without a card gets one forced show_music request
    Given the model will search the library for "the beatles"
    And the model will then write "Abbey Road would suit this stretch."
    And the model obeys the tool it is told to call and otherwise answers in words
    When the driver taps Drive Tracks
    Then the last request to the model forced the tool "show_music"
    And exactly one album card came back, for album 42

  Scenario: A question the traveller types is never forced
    Given the model will write "Fuel is ahead at Birch Gate."
    When the trip-keeper types "Where is the next fuel?" to Chester
    Then no request to the model forced a tool

  Scenario: With the library down the page says so plainly and shows no card
    Given the library for Drive Tracks is down
    And the model obeys the tool it is told to call and otherwise answers in words
    When the driver taps Drive Tracks
    Then the card is told to start no album
    And no request to the model forced the tool "show_music"
