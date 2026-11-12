Feature: A visitor who is not signed in can hear the tracks played on the active ride
  Kris, 2026-11-12: "Let them hear it". A visitor may play only a track that was
  played on the active ride, from the Listen Inn panel. There is one route,
  GET /tracks/{id}/audio, where {id} is an opaque name for a play. It checks
  the play, then hands on the library's own short-lived address for exactly that
  track. The library key never leaves the server, the album number is never
  shown, and a visitor can neither search nor browse the library. PUBLIC_LISTEN
  turns it off with no code change; a small brake stops one address hammering it.

  Background:
    Given the trip is active, the driver is signed in and RoadTracks is on

  Scenario: A visitor asking for the audio of a play on the active ride gets exactly that track
    When the driver's player reports track "Two" at index 1 of album 42 "Abbey Road" by "The Beatles" started
    And a visitor not signed in asks for the audio of the play "Two"
    Then the visitor is sent to the library's address for album 42 track 2
    And the library was asked about album 42 only

  Scenario: An id that is not a play of the active ride is 404 and the library is never asked
    When the driver's player reports track "Two" at index 1 of album 42 "Abbey Road" by "The Beatles" started
    And that play belongs to another ride
    And a visitor not signed in asks for the audio of the play "Two"
    Then the visitor is told 404
    When a visitor not signed in asks for the audio of the play "0123456789abcdef"
    Then the visitor is told 404
    And the library was never asked

  Scenario: With the switch off there is no route, no listen flag and no control
    Given public listening is switched off
    When the driver's player reports track "Two" at index 1 of album 42 "Abbey Road" by "The Beatles" started
    And a visitor not signed in asks for the audio of the play "Two"
    Then the visitor is told 404
    And the library was never asked
    And /tracks.json says listening is off
    And the home page for a visitor carries no listen control

  Scenario: Nothing a visitor sees carries the library key
    When the driver's player reports track "Two" at index 1 of album 42 "Abbey Road" by "The Beatles" started
    And a visitor not signed in asks for the audio of the play "Two"
    Then nothing a visitor was sent holds the library key or a library album address

  Scenario: The brake answers 429 after its limit
    When the driver's player reports track "Two" at index 1 of album 42 "Abbey Road" by "The Beatles" started
    And one address asks for the audio of the play "Two" 31 times
    Then the last answer is 429 with a plain sentence

  Scenario: The home page carries a Play control per playlist row when listening is on
    When the driver's player reports track "One" at index 0 of album 42 "Abbey Road" by "The Beatles" started
    And the driver's player reports track "Two" at index 1 of album 42 "Abbey Road" by "The Beatles" started
    Then /tracks.json says listening is on and every row has an opaque name
    And the home page for a visitor carries the audio element and the shared script

  Scenario: A library failure gives a plain sentence, not an error page
    Given the library is failing
    When the driver's player reports track "Two" at index 1 of album 42 "Abbey Road" by "The Beatles" started
    And a visitor not signed in asks for the audio of the play "Two"
    Then the visitor is told 502 with a plain sentence

  Scenario: A track the library cannot play is 404 with a plain sentence
    When the driver's player reports track "Three" at index 2 of album 42 "Abbey Road" by "The Beatles" started
    And a visitor not signed in asks for the audio of the play "Three"
    Then the visitor is told 404 with a plain sentence
