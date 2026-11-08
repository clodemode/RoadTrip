Feature: Drive Tracks jumps from the driver screen to the music player
  Kris, 2026-11-08: "Show that Drive Tracks button to jump to the music player."
  The driver screen's tools carry one Drive Tracks button when the music library
  is on. It opens the player on the library in a new tab, at the address the
  phone can reach. No audio passes through RoadTrip and nothing plays by itself.

  Scenario: With the library on the driver screen has a Drive Tracks button
    Given the music library is switched on
    When the driver opens the driver screen
    Then the Drive Tracks button opens the library's player in a new tab

  Scenario: The button uses the address the phone can reach
    Given the music library is switched on with the phone address "https://music.example.test:8443"
    When the driver opens the driver screen
    Then the Drive Tracks button goes to "https://music.example.test:8443/"

  Scenario: With the library off there is no button
    Given the music library is switched off
    When the driver opens the driver screen
    Then there is no Drive Tracks button
