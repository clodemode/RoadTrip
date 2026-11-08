Feature: Test-mode phone location
  A signed-in driver can start a simulated drive. While it runs, the server
  holds one simulated position for that driver, computed from the corridor,
  the seed, the start instant and the compression, so nothing ticks in a tab,
  nothing is kept in browser storage, and every page and every device signed
  in as that driver sees the same car at the same instant.

  Background:
    Given the site allows simulated drives
    And kris is signed in

  Scenario: Starting a corridor drive gives the HUD a fix without a phone
    When kris starts a simulated drive on corridor "kgn-yyz" with seed 7 and compression 60
    Then the HUD asked with no browser position reports source "fix"
    And the simulated position lies on the corridor between Kingston and Toronto

  Scenario: Every page and every device sees the same car
    Given kris started a simulated drive on corridor "kgn-yyz" with seed 7 and compression 60
    When a second device signed in as kris asks for the simulated position at the same instant
    Then both devices see the same position within 1 metre

  Scenario: The same seed replays the same drive
    Given two simulated drives on corridor "kgn-yyz" with seed 7 started at the same instant
    Then their positions 120 simulated seconds later are identical
    And a drive with seed 8 started at that instant is somewhere else

  Scenario: The car moves with time
    Given kris started a simulated drive on corridor "kgn-yyz" with seed 7 and compression 60
    Then the position 300 simulated seconds later is further along the corridor than at the start

  Scenario: Chester is handed the simulated phone
    Given kris started a simulated drive on corridor "kgn-yyz" with seed 7 and compression 60
    When kris asks Chester a question with no browser position
    Then Chester's context carries a position
    And that position is the simulated one, coarsened the same way a phone position is

  Scenario: Chester's Send my position is the simulated car, never the phone
    Given kris started a simulated drive on corridor "kgn-yyz" with seed 7 and compression 60
    When kris opens the Chester page
    Then the page says the position is simulated
    When kris asks Chester a question with no browser position
    Then that position is the simulated one, coarsened the same way a phone position is
    When kris stops the simulated drive
    And kris opens the Chester page
    Then the page does not say the position is simulated

  Scenario: The post form's My phone location is the simulated car, never the phone
    Given kris started a simulated drive on corridor "kgn-yyz" with seed 7 and compression 60
    When kris opens the post page
    Then the page says the position is simulated
    When kris posts a photo choosing My phone location with no coordinates
    Then the post is pinned by the simulated car
    When kris stops the simulated drive
    And kris opens the post page
    Then the page does not say the position is simulated

  Scenario: Stopping the drive forgets the position everywhere
    Given kris started a simulated drive on corridor "kgn-yyz" with seed 7 and compression 60
    When kris stops the simulated drive
    Then the HUD asked with no browser position reports source "plan"
    And asking for the simulated position answers that none is running

  Scenario: A site that does not allow simulated drives refuses to start one
    Given the site does not allow simulated drives
    When kris starts a simulated drive on corridor "kgn-yyz" with seed 7 and compression 60
    Then the request is refused with 404
    And asking for the simulated position answers that none is running

  Scenario: The simulated position never reaches the bus
    Given kris started a simulated drive on corridor "kgn-yyz" with seed 7 and compression 60
    When the HUD is asked with no browser position
    Then the MELT record for that HUD serve says sim true
    And it carries no latitude and no longitude
