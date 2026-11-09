Feature: An old photo with no place is pinned from the car's trail at its time, or not at all, and never checks in
  Kris, 2026-11-08: "The location of the phone during upload may not be the
  location the photo was taken from." Measured on the KATE: a photo with an
  EXIF time of 18:30 and no GPS, posted hours later, was pinned at the phone's
  present place and dated 18:30, a record that says "here at 18:30" and is
  false, and such a post could check in a stop.
  A photo whose own time is more than 20 minutes older than now and which has
  no GPS of its own says nothing about where the phone is. It is pinned from
  the car's own trail at the photo's time: the nearest in time, within 15
  minutes, of the pins earlier posts already hold. The trail is those pins and
  nothing else: no phone fix is stored on the road (telemetry goes to the bus
  with no coordinate), so the posts are the trail. With none that near the post
  has no pin. It keeps its own time, never vouches a check-in or moves the car
  whatever the phone says, and the notice, and the signed-in pin line, say it
  was taken hours ago and where it was pinned, or that it was left off the map.
  A trail point at a private place is fuzzed like any other pin. A pin the
  driver picked on the form is honoured for the pin alone and still never
  checks in; the page's own pre-selected pin source is not a choice. A fresh
  camera shot (no time, or within 20 minutes of now) still uses the phone and
  may check in, a photo with its own GPS uses it, a photo with neither time nor
  GPS is taken now and here, and nothing here stops a post from saving.
  The stops are invented, 55 km apart along a line. The earlier posts and the
  photo clocks are measured from the clock of the run.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at
    And the drive has started at A

  Scenario: An old photo is pinned from an earlier post's pin at its time
    Given a photo taken 3 hours ago with no GPS of its own
    And an earlier post pinned between A and B, made 4 minutes before the photo was taken
    And the phone reports a position at D
    When kris posts that photo
    Then the photo post is pinned between A and B
    And the photo post's pin source is "trail"
    And the photo post is dated 3 hours ago

  Scenario: The earlier post nearest in time wins
    Given a photo taken 3 hours ago with no GPS of its own
    And an earlier post pinned between A and B, made 10 minutes before the photo was taken
    And an earlier post pinned between B and C, made 4 minutes after the photo was taken
    When kris posts that photo
    Then the photo post is pinned between B and C
    And the photo post's pin source is "trail"

  Scenario: An earlier post more than 15 minutes from the photo's time is not the trail
    Given a photo taken 3 hours ago with no GPS of its own
    And an earlier post pinned between A and B, made 16 minutes before the photo was taken
    And the phone reports a position at D
    When kris posts that photo
    Then the photo post has no pin
    And the photo post is dated 3 hours ago

  Scenario: With no trail the post has no pin, whatever the phone says
    Given a photo taken 3 hours ago with no GPS of its own
    And the phone reports a position at D
    When kris posts that photo
    Then the photo post has no pin
    And the photo post exists
    And no stop was checked in

  Scenario: An old photo never checks in, though the phone is at a stop
    Given a photo taken 3 hours ago with no GPS of its own
    And an earlier post pinned at B, made 3 minutes after the photo was taken
    And the phone reports a position at B
    When kris posts that photo
    Then the photo post exists
    And B is still not arrived at
    And no stop was checked in

  Scenario: The notice says where an old photo was pinned
    Given a photo taken 3 hours ago with no GPS of its own
    And an earlier post pinned between A and B, made 4 minutes before the photo was taken
    When kris posts that photo
    Then the notice says the photo was taken 3 hours ago and pinned where the car was then
    And the notice says nothing was checked in
    And the pin line says the photo was taken 3 hours ago and pinned where the car was then

  Scenario: The notice says an old photo was left off the map
    Given a photo taken 3 hours ago with no GPS of its own
    When kris posts that photo
    Then the notice says the photo was taken 3 hours ago and left off the map
    And the notice says nothing was checked in
    And the pin line says the photo was taken 3 hours ago and left off the map

  Scenario: A trail point at a private place is fuzzed
    Given stop C is a private place
    And a photo taken 3 hours ago with no GPS of its own
    And an earlier post pinned exactly near C, made 5 minutes before the photo was taken
    When kris posts that photo
    Then the photo post is pinned on C's published point
    And the photo post is not pinned at the earlier post's exact point
    And the pin line shows no exact point

  Scenario: A fresh camera shot with no time still uses the phone and may check in
    Given a photo with no GPS of its own
    And the phone reports a position at B
    When kris posts that photo
    Then the photo post's pin source is "browser"
    And B is arrived at

  Scenario Outline: The 20 minute boundary
    Given a photo taken <age> minutes ago with no GPS of its own
    And the phone reports a position at B
    When kris posts that photo
    Then the photo post's pin source is "<source>"
    And B is <arrival>

    Examples:
      | age | source  | arrival                |
      | 19  | browser | arrived at             |
      | 21  | none    | still not arrived at   |

  Scenario: A library photo with its own GPS still uses it
    Given a photo taken 3 hours ago with its GPS at C
    And an earlier post pinned between A and B, made 4 minutes before the photo was taken
    When kris posts that photo
    Then the photo post is pinned on C's published point
    And the photo post's pin source is "exif"

  Scenario: A photo with neither time nor GPS is taken now and here
    Given a photo with no GPS of its own
    And the phone reports a position at B
    When kris posts that photo
    Then the photo post's pin source is "browser"
    And the photo post is dated now

  Scenario: A pin source the driver picked is honoured for the pin and never checks in
    Given a photo taken 3 hours ago with no GPS of its own
    And an earlier post pinned between A and B, made 4 minutes before the photo was taken
    And the phone reports a position at B
    When kris posts that photo choosing the phone's location
    Then the photo post is pinned at the phone's position
    And the photo post's pin source is "browser"
    And B is still not arrived at
    And the notice says nothing was checked in

  Scenario: The page's own pre-selected pin source is not a choice
    Given a photo taken 3 hours ago with no GPS of its own
    And an earlier post pinned between A and B, made 4 minutes before the photo was taken
    And the phone reports a position at B
    When kris posts that photo with the pin source the page pre-selected
    Then the photo post is pinned between A and B
    And the photo post's pin source is "trail"
    And B is still not arrived at

  Scenario: Choosing no pin leaves an old photo off the map
    Given a photo taken 3 hours ago with no GPS of its own
    And an earlier post pinned between A and B, made 4 minutes before the photo was taken
    When kris posts that photo choosing no pin
    Then the photo post has no pin

  Scenario: The post form says what will happen to an old photo
    When kris opens the post form
    Then the form script reads the photo's time and says an old photo is pinned from the car's trail
    And the form script marks a pin source it pre-selected itself
