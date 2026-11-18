Feature: Map markers are big enough to see, and a photo sits on top
  Kris, 2026-11-18, looking at the finished Manor ride: "pins are very small.
  Notes are kinda small ... some may be deemed better on top like photos."
  A dropped pin was an 18 px dot and a note a 34 px dot with a 12 px glyph.
  Every marker on the Ride map is now at least 30 px across, and where two
  overlap the photo is drawn above the note, the music note and the pin.

  Kris, the same afternoon: "dropped pin should be much smaller. Keep them at
  18px. All dropped pin tries to do is mark the road I am on ... photos tries to
  show a photo. Photos should be even larger as room permits." A dropped pin
  stays an 18 px dot. Then: "without obscuring too much map detail, so try
  Photos around 64px max to start." A photo is 64 px, never more.

  Scenario: Notes and music notes are big enough to see, and a photo is the biggest
    When the Ride map's script is read
    Then the photo marker is at least 56 px across
    And the photo marker is at most 64 px across
    And the note marker is at least 36 px across
    And the music note marker is at least 30 px across

  Scenario: A dropped pin is a small dot that only marks the road
    When the Ride map's script is read
    Then the dropped pin marker is at most 18 px across

  Scenario: A photo is drawn above every other marker
    When the Ride map's script is read
    Then the photo marker is drawn above the note marker
    And the note marker is drawn above the music note marker
    And the music note marker is drawn above the dropped pin marker
