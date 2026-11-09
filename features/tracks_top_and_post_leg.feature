Feature: The RoadTracks card is at the top of the driver screen and a post finds its own leg
  Kris, 2026-11-08, on the phone: the RoadTracks card was nowhere in sight,
  buried at the bottom of the middle column, and posting said to pick a leg
  when the app already knew which leg the drive is on. The card is now one
  full-width block directly under the gauges, with the player, outside every
  mode section. A post whose leg is not in the active trip lands on the leg a
  new post lands on by default; a valid leg stays, a checkpoint still wins.

  Scenario Outline: The card is on the driver screen once, before the road section
    Given the card library is on
    And the card viewer is in the "<mode>" mode
    Then the card is on the driver page once, before the road section

    Examples:
      | mode     |
      | trip     |
      | destined |
      | aimless  |

  Scenario: The card is absent when the library is off
    Given the card library is off
    Then the driver page has no card

  Scenario: A post carrying a leg from another trip lands on the default leg
    Given kris is signed in
    And the active trip has two legs
    When kris posts a photo carrying a leg id from another trip
    Then the post is accepted
    And the post is on the first leg

  Scenario: A post with a valid leg id stays on that leg
    Given kris is signed in
    And the active trip has two legs
    When kris posts a photo carrying the id of the second leg
    Then the post is accepted
    And the post is on the second leg
