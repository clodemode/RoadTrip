Feature: Start the drive, then every photo checks in wherever the car got to
  The trip's first stop is where the car leaves from, so it is never a
  check-in: until the drive is started the post form asks to Start the drive,
  with a photo as the evidence. After that there is nothing to choose. Kris
  takes or uploads a photo and posts it; the app works out the rest from a
  position: the photo's own GPS (a library upload keeps full metadata), else
  the phone's location, else (on a KATE only, where it goes first) the
  simulated car. If that position is within 10 km of an open stop, the
  nearest such stop is checked in at the photo's time, and every earlier stop
  still open is skipped: "missed" when it lies near the way the car came
  (within 15 km of the line from the last check-in to here), "rerouted" when
  it does not. A photo near no open stop is just a photo. Nothing is ever
  stamped without a position. The bus says what was decided, by which
  source, in rounded kilometres, never a coordinate and never a name.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at

  Scenario: Before the drive starts the form asks to start it
    When kris opens the post form
    Then the form offers "Start the drive from A"
    And the form offers no stop to choose

  Scenario: Starting the drive needs a photo
    When kris starts the drive with no photo
    Then the request is refused
    And A is still not arrived at

  Scenario: A photo taken at A starts the drive at the photo's time
    Given a photo taken at 09:15 with its GPS at A
    When kris starts the drive with that photo
    Then A is departed at 09:15 in A's zone
    And the bus carries a drive_started record whose source is "photo" with no latitude and no longitude

  Scenario: A note with no photo checks in nothing, whatever the phone says
    Given the drive has started at A
    And the phone reports a position at B
    When kris posts a text-only note
    Then the note was saved
    And B is still not arrived at

  Scenario: A photo taken far from A does not start the drive
    Given a photo taken at 09:15 with its GPS 60 km from A
    When kris starts the drive with that photo
    Then the request is refused with a message naming A and the distance
    And A is still not arrived at

  Scenario: Once started, the form is just a photo
    Given the drive has started at A
    When kris opens the post form
    Then the form does not offer to start the drive
    And the form offers no stop to choose
    And the form says the next stop is B

  Scenario: A photo at the next stop checks in there
    Given the drive has started at A
    And a photo taken at 10:42 with its GPS at B
    When kris posts that photo
    Then B is arrived at 10:42 in B's zone
    And the post exists
    And the bus carries a checkin_verified record for B whose source is "photo" with no latitude and no longitude

  Scenario: The phone vouches when the photo has no GPS
    Given the drive has started at A
    And a photo with no GPS of its own
    And the phone reports a position at B
    When kris posts that photo
    Then B is arrived at
    And the bus carries a checkin_verified record for B whose source is "phone"

  Scenario: A photo near no stop is just a photo
    Given the drive has started at A
    And a photo taken at 10:42 with its GPS 30 km from every stop
    When kris posts that photo
    Then the post exists
    And B is still not arrived at
    And the notice says it was posted as a photo and names B as next

  Scenario: A photo with no position is just a photo
    Given the drive has started at A
    And a photo with no GPS of its own
    When kris posts that photo
    Then the post exists
    And B is still not arrived at

  Scenario: Driving past a stop marks it missed
    Given the drive has started at A
    And a photo taken at 11:30 with its GPS at C
    When kris posts that photo
    Then C is arrived at 11:30 in C's zone
    And B is skipped as "missed" and not arrived at
    And the bus carries a checkin_skipped record for B whose reason is "missed" with no latitude and no longitude

  Scenario: A stop far off the way the car came was rerouted around
    Given the active trip has stops A, B, C and D where B lies 40 km off the line from A to C
    And the drive has started at A
    And a photo taken at 11:30 with its GPS at C
    When kris posts that photo
    Then C is arrived at 11:30 in C's zone
    And B is skipped as "rerouted" and not arrived at

  Scenario: The notice says what was decided
    Given the drive has started at A
    And a photo taken at 11:30 with its GPS at C
    When kris posts that photo
    Then the notice says C was reached and B was missed

  Scenario: A stop already passed is never checked in later
    Given the drive has started at A
    And B is skipped
    And C is arrived at
    And a photo taken at 12:10 with its GPS at B
    When kris posts that photo
    Then the post exists
    And B is still skipped and not arrived at

  Scenario: On a KATE the simulated car vouches before the photo
    Given simulation is allowed
    And the drive has started at A
    And kris's simulated car is at B
    And a photo taken at 10:42 with its GPS 900 km from every stop
    When kris posts that photo
    Then B is arrived at
    And the bus carries a checkin_verified record for B whose source is "sim"

  Scenario: On a KATE starting the drive starts the simulated car from A
    Given simulation is allowed
    And a photo with no GPS of its own
    When kris starts the drive with that photo
    Then A is departed
    And kris's simulated car is running
    And the bus carries a drive_started record whose source is "sim"

  Scenario: Off a KATE the simulated car never vouches
    Given simulation is not allowed
    And the drive has started at A
    And a photo with no GPS of its own
    When kris posts that photo
    Then B is still not arrived at

  Scenario: On a KATE the Start form offers Real GPS or Simulated car
    Given simulation is allowed
    When kris opens the post form
    Then the form offers "Real GPS"
    And the form offers "Simulated car"

  Scenario: Off a KATE the Start form offers no position choice
    Given simulation is not allowed
    When kris opens the post form
    Then the form does not offer "Real GPS"

  Scenario: On a KATE Real GPS starts the drive at the photo's GPS and starts no simulated car
    Given simulation is allowed
    And a photo taken at 09:15 with its GPS at A
    When kris starts the drive with that photo by real GPS
    Then A is departed at 09:15 in A's zone
    And kris's simulated car is not running
    And the bus carries a drive_started record whose source is "photo"

  Scenario: On a KATE Real GPS with no position is refused
    Given simulation is allowed
    And a photo with no GPS of its own
    When kris starts the drive with that photo by real GPS
    Then the request is refused
    And A is still not arrived at

  Scenario: On a KATE Real GPS stops a simulated car left running
    Given simulation is allowed
    And kris's simulated car is at B
    And a photo taken at 09:15 with its GPS at A
    When kris starts the drive with that photo by real GPS
    Then A is departed
    And kris's simulated car is not running
    And the bus carries a drive_started record whose source is "photo"
