Feature: Every stop is navigable before a trip goes live
  (Kris, 2026-11-18, made late to a 9:00 appointment: "Must use precise
  addresses for navigation.") A stop is a destination and needs a precise
  address. A stop's point is a guess near the town, so a Navigate link built
  from it is the defect. A trip push names every stop that has no address,
  making the trip active is refused while one remains, and a stop with no
  address shows no Navigate link at all, never a link to a guess. The one
  escape is the trip document marking the stop navigate-by-point (a trailhead,
  a border crossing). A routing point is a swing-past point, never a stop, and
  is not held to the address rule. The check applies at push and at
  activation, never at boot. Every place and address here is invented.

  Background:
    Given the trip-keeper pushes with the push token and is signed in

  Scenario: A pushed trip with a stop that has no address is stored and its answer names the stop
    Given a trip document whose stops carry no address and no navigate mark
    When the trip is pushed
    Then the trip-keeper's push is taken
    And the answer lists "Birch Gate", "Cedar Camp", "Dogwood" and "Elm" as needing an address
    And the trip is on the site's books

  Scenario: Making that trip active is refused and names the stop
    Given a trip document whose stops carry no address and no navigate mark
    When the trip is pushed and made active
    Then the trip-keeper's push is refused with a 409
    And the answer lists "Birch Gate", "Cedar Camp", "Dogwood" and "Elm" as needing an address
    And the trip is on the site's books
    And the site is not showing the trip
    When the trip-keeper makes the trip active by name
    Then the activation is refused with a 409
    And the site is not showing the trip

  Scenario: A stop marked navigate by point needs no address
    Given a trip document whose stops carry no address and no navigate mark
    And the stop "Birch Gate" is marked navigate by point
    When the trip is pushed
    Then the trip-keeper's push is taken
    And the answer lists "Cedar Camp", "Dogwood" and "Elm" as needing an address
    And the answer does not list "Birch Gate"

  Scenario: A routing point needs no address
    Given a trip document with routing points whose stops carry no address and no navigate mark
    When the trip is pushed
    Then the trip-keeper's push is taken
    And the answer does not list "Plum Bridge"
    And the answer does not list "Quince Turn"
    And the answer lists at least one stop

  Scenario: A stop with no address shows no Navigate link on the driver screen, the Ride page or the day's end
    Given a trip document whose stops carry no address and no navigate mark
    And the trip was made the site's ride before this rule, and the drive has started
    When the trip-keeper opens the driver screen and the Ride page
    Then neither carries a Navigate link to any navigator
    And the driver screen tells the trip-keeper "No address on file"
    And the Ride page says "No address on file" for the day's end and for the stops

  Scenario: With every address on file the trip goes live
    Given a trip document whose stops carry no address and no navigate mark
    And the stop "Birch Gate" is marked navigate by point
    When the trip is pushed
    And the overlay is pushed by ride path with addresses for "Cedar Camp", "Dogwood" and "Elm"
    And the trip-keeper makes the trip active by name
    Then the activation is taken
    And the site is showing the trip
    And a re-push of the trip with activate answers an empty list of stops needing an address
