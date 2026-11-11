Feature: On a KATE the simulated car moves one step per post, not by the clock
  Kris, 2026-11-07: "Rather then simulate a fixed driving speed, we should
  simulate by iteration. Post at start, next post is part way to first
  waypoint, then next post is at first waypoint, etc".
  A clock-driven car runs away while the driver is not posting (it reached
  Toronto 70 minutes before his next photo). So Start the drive on a KATE puts
  the simulated car at the start, and every later post first moves it one step
  along the planned route and then uses that position: halfway to the next
  stop, then at that stop, then halfway to the one after, and so on. The
  driver screen shows the car where the last post put it. After the last stop
  the car stays there. The clock-driven drive (/driver?sim=...) stays for
  testing the driver screen and is never what Start the drive starts.

  Background:
    Given kris is signed in
    And simulation is allowed
    And the active trip has stops A, B, C and D in order along a line, none arrived at

  Scenario: Starting the drive puts the stepping car at the start
    Given a photo with no GPS of its own
    When kris starts the drive with that photo
    Then A is departed
    And kris's simulated car is stepping
    And kris's simulated car is at A
    And the post is pinned at A

  Scenario: The next post lands halfway to the first stop and checks in nowhere
    Given the drive has started at A with the stepping car
    And a photo with no GPS of its own
    When kris posts that photo
    Then the post is pinned halfway along the route from A to B
    And B is still not arrived at

  Scenario: The post after that lands at the first stop and checks in there
    Given the drive has started at A with the stepping car
    And kris has posted 1 photo since the start
    And a photo with no GPS of its own
    When kris posts that photo
    Then B is arrived at
    And the post is pinned at B
    And the notice says B was reached by the simulated car

  Scenario: Steps follow the planned route, stop by stop
    Given the drive has started at A with the stepping car
    When kris posts 4 photos
    Then B and C are arrived at
    And no stop was skipped
    And kris's simulated car is at C

  Scenario: A note sent with the phone's position counts as a post
    Given the drive has started at A with the stepping car
    And kris has posted 1 photo since the start
    And the phone reports a position at B
    When kris posts a text-only note
    Then the note was saved
    And B is arrived at

  Scenario: A note with no photo and no phone position does not move the car
    Given the drive has started at A with the stepping car
    When kris posts a text-only note
    Then the note was saved
    And kris's simulated car is at A

  Scenario: The car does not move with the clock
    Given the drive has started at A with the stepping car
    When an hour passes with no post
    Then kris's simulated car is at A

  Scenario: The driver screen shows the car where the last post put it
    Given the drive has started at A with the stepping car
    And kris has posted 1 photo since the start
    When kris asks for the driver screen's figures
    Then the next stop is B
    And the miles to B are about half the miles from A to B

  Scenario: After the last stop the car stays there
    Given the drive has started at A with the stepping car
    When kris posts 8 photos
    Then D is arrived at
    And kris's simulated car is at D

  Scenario: A stop resolved another way is stepped past
    Given the drive has started at A with the stepping car
    And B is skipped
    And a photo with no GPS of its own
    When kris posts that photo
    Then the post is pinned halfway along the route from A to C
