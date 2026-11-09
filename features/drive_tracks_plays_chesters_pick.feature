Feature: Drive Tracks asks Chester for one album and plays it on the RoadTracks card
  Kris, 2026-11-08: "when I clicked on Drive Tracks, it still opened a new browser
  to Music Player instead of playing through the RoadTrip app interface." One tap
  on the driver screen asks Chester for one album from the library that fits the
  moment, and the card player on the same page plays it. This replaces the old
  button that opened the library in a new tab, and the old line that the album
  page opens instead of playing. Still true: the tap is the only thing that
  starts music; the question is built by the site and is never a preset button;
  Chester is never sent the private overlay or a raw coordinate; no test calls the
  real API.

  Background:
    Given the music library holds "Abbey Road" by "The Beatles" as album 42, playable
    And the trip-keeper is signed in with Chester and RoadTracks on

  Scenario: The button is a plain button that leaves nothing
    When the driver opens the driver screen
    Then Drive Tracks is a button with no address and no new tab
    And the same page holds the card player
    And the driver screen's script opens no window and goes to no address

  Scenario: With the library off there is no button
    Given RoadTracks is switched off
    When the driver opens the driver screen
    Then there is no Drive Tracks button

  Scenario: A tap starts Chester's album on the card
    Given the model will search the library for "the beatles"
    And the model will then show the album "m1" because "Familiar and unhurried for the open road"
    And the model will then write "Abbey Road suits the open road."
    When the driver taps Drive Tracks
    Then the card is told to start album 42 "Abbey Road" by "The Beatles"
    And the card player's own album route hands over that album's first track to play
    And Chester was asked once, in quick mode, in three rounds
    And the question was logged with the preset "drivetracks" and the album it picked

  Scenario: Only one album is ever picked
    Given the music library also holds "Revolver" by "The Beatles" as album 43, playable
    And the model will search the library for "the beatles"
    And the model will then show the albums "m1" and "m2" because "Both fit"
    And the model will then write "Two fit."
    When the driver taps Drive Tracks
    Then exactly one album card came back, for album 42

  Scenario: The question carries what the traveller last typed when it is recent
    Given the traveller typed "Something for the long hills" to Chester 30 minutes ago
    And the model will search the library for "the beatles"
    And the model will then show the album "m1" because "Familiar"
    And the model will then write "Abbey Road."
    When the driver taps Drive Tracks
    Then the question sent holds "The traveller last typed: Something for the long hills"

  Scenario: A typed question from three hours ago is not mentioned
    Given the traveller typed "Something for the long hills" to Chester 3 hours ago
    And the model will search the library for "the beatles"
    And the model will then show the album "m1" because "Familiar"
    And the model will then write "Abbey Road."
    When the driver taps Drive Tracks
    Then the question sent does not hold "The traveller last typed"

  Scenario: Albums already picked today are named so Chester picks another
    Given Drive Tracks already picked "Revolver" by "The Beatles" earlier today
    And the model will search the library for "the beatles"
    And the model will then show the album "m1" because "Familiar"
    And the model will then write "Abbey Road."
    When the driver taps Drive Tracks
    Then the question sent holds "Albums already picked today, so pick a different one: Revolver by The Beatles"

  Scenario: An album the library cannot play is never offered
    Given the music library also holds "Revolver" by "The Beatles" as album 43, not playable
    And the model will search the library for "the beatles"
    And the model will then show the album "m1" because "Familiar"
    And the model will then write "Abbey Road."
    When the driver taps Drive Tracks
    Then Chester's search result held "Abbey Road" and not "Revolver"
    And the card is told to start album 42 "Abbey Road" by "The Beatles"

  Scenario: Chester's refusal reaches the card as its own sentence
    Given today's Chester limit is already spent
    When the driver taps Drive Tracks
    Then the card shows the sentence "Chester has reached today's limit"
    And the card is told to start no album

  Scenario: No album found says so and points at the search box
    Given the model will write "Nothing in the library fits that."
    When the driver taps Drive Tracks
    Then the card shows the sentence "Chester found no album; search below"
    And the card is told to start no album

  Scenario: The library being down says so
    Given the library for Drive Tracks is down
    And the model will search the library for "the beatles"
    And the model will then write "The music library is unavailable right now."
    When the driver taps Drive Tracks
    Then the card shows the sentence "Music library unavailable"
    And the card is told to start no album

  Scenario: The question is not a button on the Chester page and no secret leaves
    Given the model will search the library for "the beatles"
    And the model will then show the album "m1" because "Familiar"
    And the model will then write "Abbey Road."
    When the driver taps Drive Tracks
    Then the Chester page offers no Drive Tracks question
    And nothing sent to the model holds the library key or address and the card's answer holds no key
