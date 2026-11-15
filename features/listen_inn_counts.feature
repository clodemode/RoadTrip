Feature: Listen Inn counts how many people tuned in and how many songs they heard, without tracking anyone
  Kris, 2026-11-14. A song is counted each time a visitor is sent to a track's
  audio (one per 302). A listener is a count of distinct visitors in a day, told
  apart by a hash of the address salted with a random value that is made fresh
  each day, lives only in memory and is never written anywhere; the hash is never
  shown. Only the two totals per ride per day are stored. The signed-in
  trip-keeper's own plays are not counted. The trip page says "N people tuned in,
  M songs heard" in plain words, and the finished trip's record keeps saying it.

  Background:
    Given the trip is active, RoadTracks is on and a track has been played on it
    And the driver is signed in

  Scenario: Three fetches from one address count one listener and three songs
    When the address 203.0.113.7 asks for the track's audio 3 times
    Then the trip page says "1 person tuned in, 3 songs heard"

  Scenario: A second address makes two listeners
    When the address 203.0.113.7 asks for the track's audio 3 times
    And the address 198.51.100.23 asks for the track's audio once
    Then the trip page says "2 people tuned in, 4 songs heard"

  Scenario: The trip-keeper is not counted
    When the signed-in driver asks for the track's audio 2 times
    And the address 203.0.113.7 asks for the track's audio once
    Then the trip page says "1 person tuned in, 1 song heard"

  Scenario: A refused or failed request is not a song
    When the address 203.0.113.7 asks for an audio name that is not a play
    Then the trip page says nothing about listeners

  Scenario: Nothing stored holds the address
    When the address 203.0.113.7 asks for the track's audio 3 times
    And the address 198.51.100.23 asks for the track's audio once
    Then nothing stored holds the address 203.0.113.7
    And nothing stored holds the address 198.51.100.23
    And the counts are kept per ride per day as two totals

  Scenario: The next day the same address is a new listener, from a new salt
    When the address 203.0.113.7 asks for the track's audio once
    And the next day comes
    And the address 203.0.113.7 asks for the track's audio once
    Then the day's salt is not the one from the day before
    And the counts show 2 listeners and 2 songs across the two days

  Scenario: The finished trip's record still shows the count
    When the address 203.0.113.7 asks for the track's audio 2 times
    And the trip is finished
    Then the trip page says "1 person tuned in, 2 songs heard"
