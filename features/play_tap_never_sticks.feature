Feature: A tap on Play always ends in music or a plain sentence
  Kris, 2026-11-10 on the road: Play was pressed and nothing happened, the
  music took a long time to start, and the Drive Tracks card went missing
  until he signed out and in again. After any tap the card either plays or
  says in plain words what happened and what to do; it never sits silent.
  The player also tells the server when something goes wrong, with an event
  word and a few numbers, never a place, a title or an address.

  # --- Play after a page load, under node -------------------------------------

  Scenario: Play with nothing loaded picks up where the listener left off
    Given the shell is idle with a resume point for album 42 at track 2
    When the listener presses Play
    Then sound is unlocked in the tap before the album is asked for
    And the audio is playing track 2
    And the Resume button is hidden

  Scenario: Play with nothing loaded and no resume point says what to do
    Given the shell is idle
    When the listener presses Play
    Then the player card says "Nothing to play yet. Ask Chester for an album."
    And the player card never said "Tap Play to start"

  # --- the watchdog, under node (timers shortened) ----------------------------

  Scenario: A stream that never starts tries once more, then stops with a plain sentence
    Given the music never starts playing
    When an album of 4 tracks is started
    And the watchdog has had time to give up
    Then the player card said "Still trying to reach the music…"
    And the album was asked for again once
    And the audio is stopped and the card says "Could not reach the music. Tap Play to try again."
    And the Play button reads "Play"
    And the player reported "play_timeout" with code "stream"

  Scenario: A stream that stalls mid-play says Buffering, and the line clears when sound returns
    Given an album of 4 tracks is playing in the shell
    When the stream stalls
    Then the player card says "Buffering…"
    When the stream plays again
    Then the player card says ""

  Scenario: An album request that never answers ends in a plain sentence, and a tap meanwhile is answered
    Given the music library never answers
    When an album of 4 tracks is started
    And the listener presses Play
    Then the player card said "Still loading…"
    When the watchdog has had time to give up
    Then the audio is stopped and the card says "Could not reach the music. Tap Play to try again."
    And the player reported "album_fetch_failed" with code "timeout"

  Scenario: Asking Chester that never answers frees the button with a plain sentence
    When the driver asks Chester and Chester never answers
    Then the card is told "Chester did not answer. Tap to ask again."
    And the ask is over so the button can be pressed again
    And the player reported the ask timed out

  # --- the look-ahead downloader, under node ----------------------------------

  Scenario: The look-ahead waits for the first sound and never downloads the playing track
    Given an album of 6 tracks is playing in the shell
    Then the playing track was never downloaded
    And the first download began after the first sound

  Scenario: Storage that refuses to share goes cold at once, without asking for the album again
    Given the library's storage refuses cross-origin downloads
    When an album of 6 tracks is started
    Then the album was asked for once only
    And the player reported "nest_cold" with code "cors_or_offline"

  # --- failure reports, under node --------------------------------------------

  Scenario: The first sound is reported with how long it took, and nothing else
    Given an album of 4 tracks is playing in the shell
    Then the player reported "first_sound" with a time in milliseconds
    And every report is a form post kept alive past the page, carrying only event, ms, code, album_id and track_idx

  # --- the card comes back by itself ------------------------------------------

  Scenario: A signed-in rider whose page lost its player frame gets the card back on the next page load, without signing in again
    Given a continuous-music trip with a music library
    And the continuous-music trip-keeper Kris is signed in
    When the continuous-music drive is started
    And Kris loads "/plan?embed=1" as a whole page
    Then the answer redirects to "/plan" with no embed=1
    When Kris loads "/plan" as a whole page
    Then the page is the player shell with exactly one audio element and one frame

  Scenario: A browser that does not say where a page loads keeps today's frame behaviour
    Given a continuous-music trip with a music library
    And the continuous-music trip-keeper Kris is signed in
    When the continuous-music drive is started
    And Kris opens the page "/plan?embed=1"
    Then the page has no audio element, no Drive Tracks card and no frame

  Scenario: A page in the top window that still says embed=1 reports it and loads again without it
    Given the frame script runs in the top window on "/plan?embed=1&x=2"
    Then the page is loaded again at "/plan?x=2"
    And the frame script reported "card_missing"

  Scenario: The sign-in page in the top window is left alone
    Given the frame script runs in the top window on the sign-in page "/driver/login?embed=1"
    Then the page is not loaded again

  Scenario: Links in a page that is not in a frame do not gain embed=1
    Given the frame script runs in the top window on "/photos"
    Then a link to "/plan" in the top window is left as "/plan"
