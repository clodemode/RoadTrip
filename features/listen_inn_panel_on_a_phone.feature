Feature: The Listen Inn list fits a phone
  The open Listen Inn list used to cover most of the map on a phone, repeated
  an album's blurb on every track and cut a long blurb in the middle of a word.
  Now an album's blurb is shown once, a long blurb ends on a whole word with an
  ellipsis. Since 2026-11-16 the Listen Inn is a card of its own, off the map:
  how tall its open list may be on a phone is in listen_inn_floating_card.feature.

  Scenario: Two tracks of one album show the album's blurb once
    Given Listen Inn lists the tracks "Song One" and "Song Two" of album "Blue Album" by "Some Band" with the blurb "A long open road and an easy tempo."
    Then the list shows the blurb "A long open road and an easy tempo." 1 time
    And the list shows both tracks

  Scenario: Tracks of two albums show each album's blurb once
    Given Listen Inn lists the tracks "Song One" and "Song Two" of album "Blue Album" by "Some Band" with the blurb "A long open road and an easy tempo."
    And Listen Inn also lists the track "Song Three" of album "Red Album" by "Other Band" with the blurb "Rain on the windshield."
    Then the list shows the blurb "A long open road and an easy tempo." 1 time
    And the list shows the blurb "Rain on the windshield." 1 time

  Scenario: A long blurb ends on a whole word with an ellipsis
    Given Listen Inn lists the track "Song One" of album "Blue Album" by "Some Band" with a blurb of 40 words
    Then the blurb ends on a whole word followed by an ellipsis
    And the blurb is at most 141 characters

  Scenario: A short blurb is shown whole
    Given Listen Inn lists the track "Song One" of album "Blue Album" by "Some Band" with the blurb "Short and sweet."
    Then the list shows the blurb "Short and sweet." 1 time

  Scenario: A long note from a pick is stored on a whole word
    Given the trip is active and the driver is signed in with RoadTracks on
    And Drive Tracks picked album 42 "Abbey Road" by "The Beatles" because "word01 word02 word03 word04 word05 word06 word07 word08 word09 word10 word11 word12 word13 word14 word15 word16 word17 word18 word19 word20 word21 word22 word23 word24 word25 word26 word27 word28 word29 word30 word31 word32 word33 word34 word35 word36 word37 word38 word39 word40 word41 word42 word43 word44 word45 word46 word47 word48 word49 word50 word51 word52 word53 word54 word55 word56 word57 word58 word59 word60 word61 word62 word63 word64 word65 word66 word67 word68 word69 word70 word71 word72 word73 word74 word75 word76 word77 word78 word79 word80"
    When the driver's player reports track "Come Together" of album 42 "Abbey Road" by "The Beatles" started
    Then the track "Come Together" carries a note ending on a whole word with an ellipsis, at most 300 bytes
