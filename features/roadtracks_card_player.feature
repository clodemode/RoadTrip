Feature: Searching on the RoadTracks card plays the album inside RoadTrip
  Kris, 2026-11-08: searching on the RoadTracks card plays the album inside
  RoadTrip and never moves to the music site. No audio passes through RoadTrip:
  the phone fetches it straight from the library's storage. Music starts only
  from a tap. The player lives outside the three mode sections so it survives
  a mode change, and the server holds one resume point per signed-in user.

  Background:
    Given the player library is on with the phone address "http://192.0.2.10:8810"

  Scenario: A path-only address is rewritten to the one the phone can reach
    When the driver asks for album 42
    Then the answer is ok
    And track 1 has the address "http://192.0.2.10:8810/media/t1?sig=x"
    And the album art is "http://192.0.2.10:8810/album/42/art"

  Scenario: An https address from storage passes through untouched
    When the driver asks for album 42
    Then track 2 has the address "https://cdn.example.test/t2.mp3?sig=y"

  Scenario Outline: An address that is not https or a library path becomes null
    When the driver asks for album 42
    Then track <n> has no address

    Examples:
      | n |
      | 3 |
      | 4 |

  Scenario: An unplayable track has a null address
    When the driver asks for album 42
    Then track 5 has no address

  Scenario: The library failing answers one plain word
    When the driver asks for album 99
    Then the answer is a plain error

  Scenario: An album call without signing in goes to the login page
    When nobody signed in asks for album 42
    Then the answer redirects to the login page

  Scenario: A resume point cannot be written without signing in
    When nobody signed in posts a resume point
    Then the answer is refused

  Scenario: A resume point round-trips for its own user only
    Given kris saves a resume point for album 42 at track 2 and 61.5 seconds
    Then kris sees a resume point for album 42 at track 2 and 61.5 seconds
    And maria sees no resume point

  Scenario: A recent resume point is on the driver page
    Given kris saves a resume point for album 42 at track 2 and 61.5 seconds
    Then the driver page for kris offers Resume

  Scenario: A resume point older than twelve hours is not offered
    Given kris saves a resume point for album 42 at track 2 and 61.5 seconds
    And that resume point is 13 hours old
    Then kris sees no resume point
    And the driver page for kris offers no Resume

  Scenario: A nonsense resume point is refused
    When kris posts a resume point with position "-5"
    Then the answer is refused

  Scenario Outline: The player sits outside the mode sections in every mode
    Given kris is in the "<mode>" mode
    Then the player is on the driver page outside every mode section

    Examples:
      | mode     |
      | trip     |
      | destined |
      | aimless  |

  Scenario: The library key is in no answer
    Given kris saves a resume point for album 42 at track 2 and 61.5 seconds
    Then the key is in no answer
