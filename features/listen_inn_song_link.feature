Feature: A link opens the Ride page with the Listen Inn card open on one song
  Kris, 2026-11-16: GET /listeninn opens the Ride page with the card open on the
  newest song; GET /listeninn/song/{id} opens it on one song, ready to play with
  one tap (a browser will not start sound by itself). {id} is the same opaque
  name of a play that /tracks/{id}/audio takes. The link is as open as the
  Listen Inn itself: only a song played on the active ride, and nothing at all
  while the whole site is private. Every row of the card carries its own link.

  Background:
    Given the trip is active and the driver is signed in with RoadTracks on

  Scenario: /listeninn opens the card on the newest song
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    And the driver's player reports track "Something" of album 42 "Abbey Road" by "The Beatles" started
    And a visitor follows the link "/listeninn"
    Then the link answers 200 and the card is open
    And the Play button is armed on "Something"

  Scenario: A song link opens the card armed on that song
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    And the driver's player reports track "Something" of album 42 "Abbey Road" by "The Beatles" started
    And a visitor opens the song link of "Come Together"
    Then the link answers 200 and the card is open
    And the card says "Come Together" by "The Beatles" is ready to play
    And the Play button is armed on "Come Together"
    And nothing on the page starts sound by itself

  Scenario: A pasted song link previews as the song
    Given the site's public address is known
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    And a visitor opens the song link of "Come Together"
    Then the page's own address in its head is the song link
    And the preview title and description name "Come Together" and "The Beatles"

  Scenario: A song that is not on this trip is a 404 that says so
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    And a visitor follows the link "/listeninn/song/0123456789abcdef"
    Then the link answers 404
    And the page says the song is not on this trip
    And the Play button is armed on "Come Together"

  Scenario: A song played on another ride is a 404
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    And a visitor notes the song link of "Come Together"
    And every play belongs to another ride
    And a visitor opens the noted song link
    Then the link answers 404

  Scenario: /listeninn on a trip with no songs goes to the Ride page
    When a visitor follows the link "/listeninn"
    Then the visitor is sent to "/"

  Scenario: With listening switched off the link still shows the song, with nothing to play
    Given visitors may not hear the music
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    And a visitor opens the song link of "Come Together"
    Then the link answers 200 and the card is open
    And the card names "Come Together" with no audio element and no Play button

  Scenario: While the whole site is private a song link asks for sign-in
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    And a visitor notes the song link of "Come Together"
    And the whole site is private
    And a visitor opens the noted song link
    Then the visitor is sent to the sign-in page

  Scenario: A signed-in driver following a song link sees the song beside one player
    Given the driver has left the start
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    And the driver opens the song link of "Come Together"
    Then the driver gets the player shell framing that song link
    And the framed page names "Come Together" with no audio element and no Play button

  Scenario: Every row of the card carries a link to its own song
    Then a list row for the song "a1" links to "/listeninn/song/a1"
    And a list row with no name carries no link
