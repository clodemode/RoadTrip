Feature: A photo is pinned where the phone is, and a stop passed on the route is missed, not rerouted
  Kris on the KATE, 2026-11-07: "Take photo left no gps pin. Upload photo gave
  gps then skipped all waypoints but the last." and "Take photo should accept
  gps from phone".
  A photo taken in the browser carries no GPS (Safari strips it), so its pin
  is the phone's location. On a KATE the phone's location IS the simulated
  car: the browser gives no real fix over plain HTTP, and the KATE must never
  hold Kris's real position, so while a simulated drive runs it pins every
  post and vouches every check-in, ahead of a photo's own GPS. Off a KATE the
  ladder stays: the photo's GPS, else the phone's location, else the stop.
  A stop skipped on the way is "missed" when it lies near the planned route
  between the last check-in and this one, and "rerouted" only when it lies
  off that stretch of the planned route, never by a straight line between
  them. The notice names who vouched, so a check-in decided by the simulated
  car says so.

  Background:
    Given kris is signed in

  Scenario: On a KATE a camera photo is pinned at the simulated car
    Given simulation is allowed
    And the drive has started on the Kingston to Toronto trip
    And kris's simulated car is near Belleville
    And a photo with no GPS of its own
    When kris posts that photo
    Then the post is pinned near Belleville
    And the post's pin source is "sim"

  Scenario: On a KATE a library photo's real GPS never pins or vouches
    Given simulation is allowed
    And the drive has started on the Kingston to Toronto trip
    And kris's simulated car is near Belleville
    And a photo taken with its GPS 1500 km from every stop
    When kris posts that photo
    Then the post is pinned near Belleville
    And the post is not pinned at the photo's own GPS
    And Belleville is arrived at

  Scenario: Off a KATE a camera photo is pinned at the phone's location
    Given simulation is not allowed
    And the drive has started on the Kingston to Toronto trip
    And a photo with no GPS of its own
    And the phone reports a position near Belleville
    When kris posts that photo
    Then the post is pinned near Belleville
    And the post's pin source is "browser"
    And Belleville is arrived at

  Scenario: The post form asks the phone for its location when the photo has none
    When kris opens the post form
    Then the form requests the phone's location as soon as it opens
    And the form sends the phone's position with the post when it has one

  Scenario: Stops passed along the 401 are missed, not rerouted
    Given the drive has started on the Kingston to Toronto trip
    And the phone reports a position near Toronto
    When kris posts a photo
    Then Toronto is arrived at
    And Napanee, Belleville, Trenton, Cobourg and Oshawa are skipped as "missed"

  Scenario: A stop off the planned route is rerouted around
    Given the active trip has stops A, B, C and D where B lies 40 km off the line from A to C
    And the drive has started at A
    And a photo taken at 11:30 with its GPS at C
    When kris posts that photo
    Then B is skipped as "rerouted" and not arrived at

  Scenario: The notice names the simulated car when it decided
    Given simulation is allowed
    And the drive has started on the Kingston to Toronto trip
    And kris's simulated car is near Belleville
    And a photo with no GPS of its own
    When kris posts that photo
    Then the notice says Belleville was reached by the simulated car
