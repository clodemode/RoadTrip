Feature: Chester always ends an album ask in an answer, and asks a backup model when the API is down
  Production, 2026-11-12 and 13: four Drive Tracks taps in a row ended in
  "Chester stopped before finishing. Ask again." The model spent every round
  it is allowed on searching the music library, most searches found nothing,
  and the last round was one more search with no pick and no words.
  An album ask now always ends in a card or a plain sentence: the round
  before the last tells every search there are no more, the last round has
  the search tool withdrawn, and a model that still picked nothing is shown a
  truthful sentence built by the site, never an error. The rule that only an
  album a search really returned can be picked stands.
  A search that should match does match: the library is asked for as many
  albums as it gives before RoadTrip drops the ones it cannot play, and a
  name that found nothing is tried once more with "and" spelled "&".
  Two asks the same day met a 503 from the API before any answer began, the
  SDK's own retries already spent. Such a request is asked once more on the
  backup model, a setting (CHESTER_MODEL_BACKUP, default claude-sonnet-5-5,
  "off" turns it off), and the row says so.

  Background:
    Given the library to search holds "Raising Sand" by "Robert Plant & Alison Krauss", playable

  Scenario: Every round is a search, and the tap still ends in a plain sentence
    Given RoadTracks is on for Chester
    And the model searches the library for "Nobody At All" in every round it is given
    When the driver asks Drive Tracks for an album
    Then the model was asked 3 times
    And the searches before the last round were told there are no more searches
    And the last request withdraws the search tool
    And the page shows a plain sentence saying the library was searched, and no error
    And the row has no error and says the model searched and picked no album

  Scenario: An artist spelled with "and" finds the album spelled with "&"
    Given RoadTracks is on for Chester
    And the model searches the library for "Robert Plant and Alison Krauss"
    And the model then picks "m1"
    And the model then writes "Raising Sand fits the hills."
    When the trip-keeper asks Chester for "Something for the hills"
    Then the page shows one album card for "Raising Sand"

  Scenario: A playable album past the library's first 25 matches is still found
    Given the library to search also holds 30 albums by "Johnny Cash" that cannot be played
    And the library to search holds "With His Hot And Blue Guitar" by "Johnny Cash", playable
    And RoadTracks is on for Chester
    And the model searches the library for "Johnny Cash"
    And the model then picks "m1"
    And the model then writes "Spare and steady."
    When the trip-keeper asks Chester for "Something by Johnny Cash"
    Then the page shows one album card for "With His Hot And Blue Guitar"

  Scenario: A 503 before the answer began is asked again on the backup model
    Given the API answers the first model with 503 before the answer begins
    And the backup model writes "Fuel is ahead at Birch Gate."
    When the trip-keeper asks Chester for "Where is the next fuel?"
    Then the page shows "Fuel is ahead at Birch Gate." and no error
    And request 2 asked the model "claude-sonnet-5-5"
    And the row's note says the first model was unavailable and the backup model answered
    And the row was served by "claude-sonnet-5-5" and priced at that model

  Scenario: With the backup off a 503 is said as it always was
    Given the backup model setting is "off"
    And the API answers the first model with 503 before the answer begins
    When the trip-keeper asks Chester for "Where is the next fuel?"
    Then the page shows the error "The API is having trouble right now. Ask again in a moment." once
    And the model was asked 1 time

  Scenario: A 503 on the backup model too is said once
    Given the API answers the first model with 503 before the answer begins
    And the API answers the backup model with 503 too
    When the trip-keeper asks Chester for "Where is the next fuel?"
    Then the page shows the error "The API is having trouble right now. Ask again in a moment." once
    And the model was asked 2 times
