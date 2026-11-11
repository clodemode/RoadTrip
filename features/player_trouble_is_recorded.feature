Feature: Player trouble on the phone is recorded where a signed-in person can read it
  Kris, 2026-11-10: on the road the player would not start, Play did nothing and
  nothing was written down anywhere, because production had no place to keep
  trouble records. Now the server keeps them in a file on its own disk, the
  phone reports what went wrong in a few fixed words and numbers, and a
  signed-in person reads them back. Nothing private is ever kept: no position,
  no address, no typed words, no browser name.

  Background:
    Given the trouble record is kept in a file
    And the trip-keeper is signed in

  Scenario: A stream that will not start leaves a record a signed-in person can read
    When the phone reports "play_timeout" with ms "15000", code "no_audio", album 318 and track 2
    Then the phone is answered with no content
    And the trouble record holds an errors record "play_timeout" from the phone with ms 15000, code "no_audio", album 318 and track 2
    And that record holds nothing else from the request

  Scenario: The first sound is an event, not trouble
    When the phone reports "first_sound" with ms "2100", code "", album 318 and track 0
    Then the trouble record holds an events record "first_sound" from the phone with ms 2100, code "", album 318 and track 0

  Scenario: A record with a place in it is refused
    When the phone reports "stream_error" with a lat and a lng field
    Then the trouble record holds an errors record "stream_error" from the phone with no lat and no lng
    And a server record carrying a lat field is never written

  Scenario: Unknown words are refused or dropped
    When the phone reports the unknown event "rm -rf"
    Then the phone is answered with a bad request
    When the phone reports "stream_stalled" with ms "9999999", code "Bad Code!", album -4 and track 5000
    Then the trouble record holds an errors record "stream_stalled" from the phone with no ms, no code, no album and no track

  Scenario: Nobody signed out reads or writes the record
    Then a signed-out caller cannot read the trouble record or report to it

  Scenario: With no file the record says it is off
    Given the trouble record is not kept
    Then the trouble record answers that its sink is off with no records
