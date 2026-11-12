Feature: Drive Tracks does not start with the same bands every time
  Kris, 2026-11-12: "Chester started with the same two bands again yesterday,
  Tragically Hip and Gordon Lightfoot. Chester should keep a memory and use a
  seed to avoid being predictable. Today we are in Virginia so it is curious to
  see what Chester picks." Drive Tracks now remembers the artists it picked on
  any trip and steers away from the newest twelve, naming them in the question
  and taking their albums out of the search answer so the rule does not rest on
  Chester obeying it. Each tap also draws one angle from a fixed list with a
  seed, and the seed and the angle are recorded so a pick can be explained.
  Where the car is stays in force with every angle. No test calls the real API.

  Background:
    Given the music library holds "Abbey Road" by "The Beatles" as album 42, playable
    And the trip-keeper is signed in with Chester and RoadTracks on

  Scenario: An artist picked on an earlier trip is steered away from and its albums are not offered
    Given the music library also holds "Harvest" by "Neil Young" as album 44, playable
    And Drive Tracks picked album 42 "Abbey Road" by "The Beatles" 2 days ago on another trip
    And the model will search the library for "the beatles"
    And the model will then show the album "m1" because "Familiar"
    And the model will then write "Harvest."
    When the driver taps Drive Tracks
    Then the question sent holds "so steer away from them and from every album they made: The Beatles"
    And the question sent does not hold "Albums already played on this trip"
    And the search answer Chester got says "Harvest by Neil Young"
    And the search answer Chester got names no album by "The Beatles"
    And exactly one album card came back, for album 44

  Scenario: When every match is by a recent artist Chester is told so and no card comes
    Given Drive Tracks picked album 42 "Abbey Road" by "The Beatles" 2 days ago on another trip
    And the model will search the library for "the beatles"
    And the model will then write "Nothing new."
    When the driver taps Drive Tracks
    Then the search answer Chester got says "Every match is by an artist picked lately. Search a different artist."
    And the card is told to start no album

  Scenario: An artist beyond the newest twelve may come back, its album is still kept out of this trip
    Given the music library also holds "Old One" by "Old Band" as album 90, playable
    And Drive Tracks picked "Old One" by "Old Band" long ago and then twelve other artists on this trip
    And the model will search the library for "old band"
    And the model will then show the album "m1" because "Familiar"
    And the model will then write "Abbey Road."
    When the driver taps Drive Tracks
    Then the question sent does not hold "so steer away from them and from every album they made: Old Band"
    And the question sent holds "Old One by Old Band"
    And the search answer Chester got says "Abbey Road by The Beatles"
    And the search answer Chester got names no album by "Old Band"

  Scenario: Two taps with different seeds are asked from different angles
    Given the draws for the taps are 0 and 1
    And the model will pick the first album and write "One."
    And the model will pick the first album and write "Two."
    When the driver taps Drive Tracks
    And the driver taps Drive Tracks again
    Then the two questions carry different angle sentences

  Scenario: The same seed asks the same angle
    Given the draws for the taps are 3 and 3
    And the model will pick the first album and write "One."
    And the model will pick the first album and write "Two."
    When the driver taps Drive Tracks
    And the driver taps Drive Tracks again
    Then the two questions carry the same angle sentence

  Scenario: Where the car is stays in force with every angle
    Given the draws for the taps are 0 and 1
    And the model will pick the first album and write "One."
    And the model will pick the first album and write "Two."
    When the driver taps Drive Tracks
    And the driver taps Drive Tracks again
    Then both questions say to use where the car is

  Scenario: The trouble record carries the seed and the angle
    Given the draws for the taps are 4
    And the model will pick the first album and write "One."
    When the driver taps Drive Tracks
    Then the trouble record holds a drivetracks_pick drawn with seed 4 and the angle "This time, pick a record that suits the hour and the weather."

  Scenario: A long memory does not stop the tap
    Given the music library holds albums 1 to 30 by "The Tragically Hip", all playable
    And Drive Tracks already picked albums 1 to 30 on this trip
    And the model will pick the first album and write "Abbey Road."
    When the driver taps Drive Tracks
    Then the question sent holds "Album 30 by The Tragically Hip"
    And exactly one album card came back, for album 42
