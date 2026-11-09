Feature: The start is a departure and a resolved stop is history
  The trip's first stop is where the car leaves from, so no page ever asks
  the driver to arrive there or navigate to it: before the drive starts the
  stop list points at Start the drive, and after it the start reads Left. A
  stop that is arrived at or skipped is settled: its row says what happened
  and offers no way to navigate to it or to arrive at it by hand. Only the
  next open stop is a destination, and it is the one row with navigation.
  The driver screen and Chester tell the same story. Undoing a stamp or a
  skip is the one hand correction, and it takes no position because it makes
  no claim.
  (Ruled by Kris 2026-11-07: keep a small Undo on settled rows; never offer
  navigation on the start. Amended by Kris 2026-11-09: the next open stop is
  still the one row marked next, but every open stop on the day being driven
  offers navigation too, so a navigator can be set to a point ahead without
  completing a leg; see ride_page_checkin_and_navigate.feature.)

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at

  Scenario: Before the drive starts the start row points at starting it
    When kris opens the trip page
    Then the row for A says it is where the drive leaves from
    And the row for A offers "Start the drive"
    And the row for A offers no navigation
    And no row offers "Arrived"

  Scenario: Once the drive has started the start reads Left and is settled
    Given the drive has started at A
    When kris opens the trip page
    Then the row for A says "Left"
    And the row for A offers no navigation
    And no row offers "Arrived"

  Scenario: The next open stop is marked next, and the day's other open stops are destinations too
    Given the drive has started at A
    When kris opens the trip page
    Then the row for B is marked as next and offers navigation
    And the row for C offers navigation
    And the row for D offers navigation
    And the row for C is not marked as next

  Scenario: An arrived stop is settled and the next moves on
    Given the drive has started at A
    And B is arrived at
    When kris opens the trip page
    Then the row for B says "Arrived" and offers no navigation
    And the row for C is marked as next and offers navigation

  Scenario: A skipped stop is settled
    Given the drive has started at A
    And B is skipped
    And C is arrived at
    When kris opens the trip page
    Then the row for B says "Missed" and offers no navigation
    And the row for D is marked as next and offers navigation

  Scenario: The leg page tells the same story
    Given the drive has started at A
    And B is skipped
    When kris opens the first leg's page
    Then the row for A says "Left" and offers no navigation
    And the row for B says "Missed" and offers no navigation
    And the row for C is marked as next and offers navigation

  Scenario: Every settled row still offers Chester
    Given the drive has started at A
    When kris opens the trip page
    Then the row for A offers "Ask Chester about here"

  Scenario: Only the trip start reads Left
    Given the active trip has two days and the first stop of day two is arrived at
    When kris opens the trip page
    Then the first stop of day two says "Arrived", not "Left"

  Scenario: Undo takes back a stamp without a position
    Given the drive has started at A
    And B is arrived at
    When kris undoes B
    Then B is still not arrived at
    And B is not skipped
    And the row for B is marked as next

  Scenario: Undo takes back a skip
    Given the drive has started at A
    And B is skipped
    And C is arrived at
    When kris undoes B
    Then B is not skipped
    And B is still not arrived at

  Scenario: Undoing the start re-opens starting the drive
    Given the drive has started at A
    When kris undoes A
    And kris opens the post form
    Then the form offers "Start the drive from A"

  Scenario: The driver screen names the start as the start, never as next
    When kris opens the driver screen
    Then the screen says the drive starts at A
    And the screen does not call A the next stop
    And the screen offers no navigation to A

  Scenario: Once started the driver screen names the next open stop
    Given the drive has started at A
    And B is skipped
    When kris asks for the driver screen's figures
    Then the next stop is C
    And the drive is reported as started

  Scenario: Chester is told what happened at each stop
    Given the drive has started at A
    And B is skipped
    When Chester's context is built
    Then it says A was left, not arrived at
    And it says B was passed without stopping
    And it names C as the next stop

  Scenario: The open data marks the start and the skipped stops
    Given the drive has started at A
    And B is skipped
    When kris reads the route data
    Then A is marked as the start
    And B carries its skip and its reason
    And C carries neither an arrival nor a skip
