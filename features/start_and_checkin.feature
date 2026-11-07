Feature: Start the drive, then check in at each stop with a position
  The trip's first stop is where the car leaves from, so it is never a
  check-in. Until the drive is started the post form asks for one thing:
  Start the drive, with a photo as the evidence. After that, each stop is a
  check-in, and a check-in needs a position near the stop: the photo's own
  GPS, else the phone's location, else (on a KATE only) the simulated car.
  A check-in with no position, or one too far from the stop, is refused and
  nothing is stamped. The bus says which source vouched and how far off it
  was, in rounded kilometres, never a coordinate.
  Stops are taken in order. The form offers only the NEXT stop, with two
  buttons beside it: Missed it (the stop is skipped, stamped as such, and
  the one after it becomes next) and Later (post the photo with no check-in;
  the next stop stays next). A stop cannot be checked in while an earlier
  one is neither arrived at nor skipped.

  Background:
    Given kris is signed in
    And the active trip has stops A, B and C in order, none arrived at

  Scenario: Before the drive starts the form asks to start it, not to check in
    When kris opens the post form
    Then the form offers "Start the drive from A"
    And A is not offered as a check-in
    And B and C are not offered yet

  Scenario: Starting the drive needs a photo
    When kris starts the drive with no photo
    Then the request is refused
    And A is still not arrived at

  Scenario: A photo taken at A starts the drive at the photo's time
    Given a photo taken at 09:15 with its GPS at A
    When kris starts the drive with that photo
    Then A is departed at 09:15 in A's zone
    And the bus carries a drive_started record whose source is "photo" with no latitude and no longitude

  Scenario: A photo taken far from A does not start the drive
    Given a photo taken at 09:15 with its GPS 60 km from A
    When kris starts the drive with that photo
    Then the request is refused with a message naming A and the distance
    And A is still not arrived at

  Scenario: Once started, the form offers only the next stop
    Given the drive has started at A
    When kris opens the post form
    Then the form does not offer to start the drive
    And the form offers "Next: B" with the buttons "Check in", "Missed it" and "Later"
    And C is not offered

  Scenario: A later stop cannot be checked in while an earlier one is open
    Given the drive has started at A
    And a photo taken at 10:42 with its GPS at C
    When kris posts that photo checking in at C
    Then the request is refused with a message naming B
    And C is still not arrived at
    And no post was saved

  Scenario: Missed it skips the next stop and moves on
    Given the drive has started at A
    When kris taps "Missed it" for B
    Then B is skipped and not arrived at
    And the form offers "Next: C" with the buttons "Check in", "Missed it" and "Later"
    And the bus carries a checkin_skipped record for B with no latitude and no longitude

  Scenario: Later posts the photo and keeps the next stop next
    Given the drive has started at A
    And a photo taken at 10:42 with its GPS at B
    When kris posts that photo choosing "Later"
    Then the post exists
    And B is still not arrived at
    And the form offers "Next: B" with the buttons "Check in", "Missed it" and "Later"

  Scenario: After the last stop there is nothing to check in
    Given the drive has started at A
    And B is skipped
    And C is arrived at
    When kris opens the post form
    Then the form offers no check-in

  Scenario: A check-in at B vouched for by the photo's GPS
    Given the drive has started at A
    And a photo taken at 10:42 with its GPS at B
    When kris posts that photo checking in at B
    Then B is arrived at 10:42 in B's zone
    And the bus carries a checkin_verified record for B whose source is "photo" with no latitude and no longitude

  Scenario: A check-in at B vouched for by the phone when the photo has no GPS
    Given the drive has started at A
    And a photo with no GPS of its own
    And the phone reports a position at B
    When kris posts that photo checking in at B
    Then B is arrived at
    And the bus carries a checkin_verified record for B whose source is "phone"

  Scenario: A check-in with no position at all is refused
    Given the drive has started at A
    And a photo with no GPS of its own
    When kris posts that photo checking in at B
    Then the request is refused with a message asking for a position
    And B is still not arrived at
    And no post was saved

  Scenario: A check-in far from the stop is refused
    Given the drive has started at A
    And a photo taken at 10:42 with its GPS at C
    When kris posts that photo checking in at B
    Then the request is refused with a message naming B and the distance
    And B is still not arrived at
    And the bus carries a checkin_refused record for B with no latitude and no longitude

  Scenario: On a KATE the simulated car vouches for a check-in
    Given simulation is allowed
    And the drive has started at A
    And kris's simulated car is at B
    And a photo with no GPS of its own
    When kris posts that photo checking in at B
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
    When kris posts that photo checking in at B
    Then the request is refused with a message asking for a position
