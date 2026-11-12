Feature: Anyone can see what is playing and what played, without signing in
  Kris, 2026-11-12: friends follow the trip signed out. They see a small
  "Currently playing" chip on the map and a "Listen Inn" list of what played,
  newest first, with Chester's note on each. It is text only: no audio, no
  library address, no key and no user name. A play is one track that started
  sounding on the driver's player; the same start reported twice is one play.

  Background:
    Given the trip is active and the driver is signed in with RoadTracks on

  Scenario: A track that starts sounding shows as now to a visitor who is not signed in
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    Then a visitor not signed in sees "Come Together" by "The Beatles" on "Abbey Road" as now playing
    And a visitor not signed in sees 1 track in the list

  Scenario: The same start reported twice is one play
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    And the driver's player reports the same start again
    Then a visitor not signed in sees 1 track in the list

  Scenario: A resume post with no start records nothing
    When the driver's player posts a resume point for album 42 with no start
    Then a visitor not signed in sees 0 tracks in the list

  Scenario: An album that came from a Drive Tracks pick carries Chester's reason
    Given Drive Tracks picked album 42 "Abbey Road" by "The Beatles" because "Long open road and an easy tempo."
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    Then the track "Come Together" carries the note "Long open road and an easy tempo."

  Scenario: An album that did not come from a pick carries no note
    When the driver's player reports track "Sun King" of album 77 "Other" by "Someone" started
    Then the track "Sun King" carries no note

  Scenario: A play near a private stop publishes the snapped cell and never the exact point
    Given the driver's newest pinned post is at the exact point 44.3217, -106.2789 near a private stop
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    Then the track "Come Together" is published at 44.3, -106.3
    And nothing a visitor can read holds the exact point 44.3217 or -106.2789

  Scenario: A play with no point is listed without a marker
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    Then the track "Come Together" has no point

  Scenario: The answer carries no key, no library link and no user name
    Given Drive Tracks picked album 42 "Abbey Road" by "The Beatles" because "Long open road and an easy tempo."
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    Then what a visitor can read holds no key, no library link, no album number and no user name

  Scenario: A play older than the freshness window is last played, not now
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    And that play is 30 minutes old
    Then a visitor not signed in sees "Come Together" by "The Beatles" on "Abbey Road" as last played
    And a visitor not signed in sees 1 track in the list

  Scenario: The home page carries the Currently playing chip and the Listen Inn toggle
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    Then the home page for a visitor not signed in carries the Currently playing chip and the Listen Inn toggle

  Scenario: A ride with no plays shows neither
    Then the home page for a visitor not signed in carries neither the chip nor the toggle
    And a visitor not signed in sees 0 tracks in the list
