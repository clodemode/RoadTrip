Feature: Every Drive Tracks tap ends in one album card, never in words alone
  Kris, 2026-11-14, on the road: three of four taps on "Ask Chester for an
  album" ended with words and no album, because nothing told the model it had
  to use the music tool. Kris, 2026-11-18, on the way to Manor: every tap
  failed at once, because the API stopped accepting a forced tool on the
  Chester models ("thinking.type.between_tools" is not supported, and a
  tool_choice of "tool" or "any" is refused beside adaptive thinking). So no
  request forces a tool and every request thinks adaptively: a turn that ends
  without searching is asked in words to search, and a turn that searched and
  has no card is asked in words to show one. A question the traveller types is
  never pushed. No test calls the real API; the stubbed model does what the
  last words it was sent ask, and otherwise answers in words, which is how the
  live model behaved when it was not told.

  Background:
    Given the music library holds "Abbey Road" by "The Beatles" as album 42, playable
    And the trip-keeper is signed in with Chester and RoadTracks on

  Scenario: A model that would answer in words is asked in words to search, and an album comes back
    Given the model obeys the tool it is told to call and otherwise answers in words
    When the driver taps Drive Tracks
    Then no request to the model forced a tool
    And every request to the model asked for adaptive thinking
    And exactly one album card came back, for album 42
    And the card is told to start album 42 "Abbey Road" by "The Beatles"

  Scenario: A model that searches and then ends without a card is asked in words to show one
    Given the model will search the library for "the beatles"
    And the model will then write "Abbey Road would suit this stretch."
    And the model obeys the tool it is told to call and otherwise answers in words
    When the driver taps Drive Tracks
    Then no request to the model forced a tool
    And the last request to the model asked in words for show_music
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
    And no request to the model forced a tool
