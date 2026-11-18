Feature: Map markers are big enough to see, and a photo sits on top
  Kris, 2026-11-18, looking at the finished Manor ride: "pins are very small.
  Notes are kinda small ... some may be deemed better on top like photos."
  A dropped pin was an 18 px dot and a note a 34 px dot with a 12 px glyph.
  Every marker on the Ride map is now at least 30 px across, and where two
  overlap the photo is drawn above the note, the music note and the pin.

  Scenario: Every kind of map marker is at least 30 px across
    When the Ride map's script is read
    Then the photo marker is at least 40 px across
    And the note marker is at least 36 px across
    And the music note marker is at least 30 px across
    And the dropped pin marker is at least 30 px across

  Scenario: A photo is drawn above every other marker
    When the Ride map's script is read
    Then the photo marker is drawn above the note marker
    And the note marker is drawn above the music note marker
    And the music note marker is drawn above the dropped pin marker
