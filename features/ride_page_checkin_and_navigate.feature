Feature: The Ride page checks in at any stop and navigates to any of the day's stops
  (Kris, 2026-11-09, the eve of Kingston to Hudson Bend.) Once the drive has
  started every open stop on the Ride page carries a live Check in: it opens the
  check-in post for that stop, whatever has or has not been reached before it,
  and opening it stamps nothing; the photo's position still decides what is
  arrived at and what is missed. Every open stop on the day being driven offers
  Navigate in Waze, Google Maps and Apple Maps, not only the next one: a
  navigator can be set to a point in the future without completing a leg. The
  day's end is the first target, with Add a stop beside it. A Waze or Apple
  Maps link holds one destination, so an added stop is where those two go
  first, and they move on to the day's end once the car has passed it; Google
  Maps carries the added stop as a waypoint on the way to the day's end. Nothing
  here reaches a visitor who is not signed in: no map link, no check-in link,
  no day's end. Every place here is invented.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at

  Scenario: Before the drive starts no row offers Check in
    When kris reads the Ride page
    Then no Ride row offers Check in
    And the Ride row for A offers Start the drive

  Scenario: Once started every open stop offers Check in, in any order
    Given the drive has started at A
    When kris reads the Ride page
    Then the Ride row for B offers Check in to its own post form
    And the Ride row for C offers Check in to its own post form
    And the Ride row for D offers Check in to its own post form
    And the Ride row for A offers no Check in

  Scenario: A settled stop offers no Check in
    Given the drive has started at A
    And B is arrived at
    When kris reads the Ride page
    Then the Ride row for B offers no Check in
    And the Ride row for C offers Check in to its own post form

  Scenario: Opening a stop's check-in post stamps nothing
    Given the drive has started at A
    When kris opens D's check-in post
    Then the form says it is checking in at D
    And the form pre-selects D
    And D is still not arrived at
    And B is still not arrived at

  Scenario: A check-in post out of order still obeys the physics of travel
    Given the drive has started at A
    And a photo taken at 11:30 with its GPS at C
    When kris posts that photo through D's check-in post
    Then C is arrived at 11:30 in C's zone
    And B is skipped as "missed" and not arrived at
    And D is still not arrived at

  Scenario: A check-in post with no position stamps nothing
    Given the drive has started at A
    And a photo with no GPS of its own
    When kris posts that photo through B's check-in post
    Then the post exists
    And B is still not arrived at

  Scenario: Every open stop on the day being driven offers navigation
    Given the drive has started at A
    When kris reads the Ride page
    Then the Ride row for B offers navigation
    And the Ride row for C offers navigation
    And the Ride row for D offers navigation
    And the Ride row for A offers no navigation

  Scenario: The leg page offers the same
    Given the drive has started at A
    When kris reads the first leg's page
    Then the Ride row for C offers navigation
    And the Ride row for C offers Check in to its own post form

  Scenario: The day's end is the first navigation target
    Given the drive has started at A
    When kris reads the Ride page
    Then the page offers Navigate to day's end at D
    And the day's end links go to D in Waze, Google Maps and Apple Maps
    And the page says nothing about a stop on the way

  Scenario: A private day's end is navigated by name, never by its cell
    Given the drive has started at A
    And D is a private place
    When kris reads the Ride page
    Then the page offers Navigate to day's end at D
    And the day's end links carry D's name and no coordinate

  Scenario: An added stop is where Waze and Apple Maps go first, and a waypoint for Google Maps
    Given the drive has started at A
    And kris has added the stop "Pine Fork" between B and C
    When kris reads the Ride page
    Then the page offers Navigate to day's end at D
    And the Waze and Apple Maps links go to "Pine Fork"
    And the Google Maps link goes to D by way of "Pine Fork"
    And the page says Waze and Apple Maps go to "Pine Fork" first and Google Maps carries it on the way

  Scenario: Add a stop finds a place by name and adds it to the day after a look
    Given the drive has started at A
    And the place search knows "Pine Fork"
    When kris looks up the stop "Pine Fork"
    Then the page shows the found place "Pine Fork" with an Add form
    And the place search was asked near the day's road, never at a private place
    And the day holds no routing point
    When kris confirms the found place
    Then the day holds the routing point "Pine Fork"
    And the trip still has 4 stops

  Scenario: Add a stop at a place the search does not know says so
    Given the drive has started at A
    And the place search knows nothing
    When kris looks up the stop "Nowhere Hollow"
    Then the page says no place was found
    And the day holds no routing point

  Scenario: A visitor who is not signed in gets none of it
    Given the drive has started at A
    And D is a private place
    When a visitor reads the Ride page
    Then the page carries no map link
    And the page carries no check-in link
    And the page offers no day's end
