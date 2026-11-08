Feature: Starting the drive says it worked and returns to the driver screen
  Kris, 2026-11-08: "it took forever then it returned me to the post a photo
  page ... the Start the drive button took me to a fresh form." The drive had
  started, but nothing said so. Posting shows it is working, and a start lands
  on the driver screen with a plain confirmation.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at

  Scenario: A start goes to the driver screen and says the drive started
    Given a photo taken at 09:15 with its GPS at A
    When kris starts the drive with that photo
    Then the answer goes to the driver screen
    And the driver screen confirms the drive started from A

  Scenario: An ordinary photo stays on the post form so the next one is quick
    Given the drive has started at A
    And a photo taken at 10:00 with its GPS at B
    When kris posts that photo
    Then the answer goes back to the post form

  Scenario: The page shows it is working while a post uploads
    Then the post form script says it is uploading when a post is sent
