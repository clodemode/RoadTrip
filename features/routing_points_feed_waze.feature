Feature: Routing points inform navigation
  A routing point is a place the trip-keeper wants the car to drive through,
  not a stop to check in at. It sits on a leg in order, and because a Waze link
  holds one destination and no via points, it works by being the next place the
  link goes to. Every place and coordinate here is invented.

  Scenario: A trip document carries a routing point that is not a stop
    Given a trip whose document carries routing points
    Then the trip has 5 stops
    And a routing point is not a stop to check in at

  Scenario: Navigate in Waze goes to a routing point that lies ahead of the next stop
    Given a trip whose document carries routing points
    When the car sits just after Alder on the ridge road
    Then Navigate in Waze goes to "Plum Bridge"

  Scenario: Passing a routing point moves the link on to the next one
    Given a trip whose document carries routing points
    When the car sits just after Alder on the ridge road
    Then Navigate in Waze goes to "Plum Bridge"
    And passing it moves the link on to the next one
    When the car sits past Birch Gate
    Then Navigate in Waze goes to "Quince Turn"
    When the car sits past Quince Turn
    Then Navigate in Waze goes to "Cedar Camp"

  Scenario: A routing point can be added on the road to a leg ahead
    Given a trip whose document carries no routing points
    And the trip-keeper is signed in with the plan saved
    When the trip-keeper adds the routing point "Pine Fork" to day 2 after stop 3
    Then the routing point is added
    And the trip has 5 stops
    When the car sits on day 2 before Pine Fork
    Then Navigate in Waze goes to "Pine Fork"
    And passing it moves the link on to the next one

  Scenario: The road already driven does not change when a routing point is added
    Given a trip whose document carries no routing points
    And the trip-keeper is signed in with the plan saved
    And the car has arrived at "Birch Gate"
    When the trip-keeper adds the routing point "Pine Fork" to day 1 after stop 1
    Then the routing point is refused
    And the trip has 5 stops

  Scenario: On the leg being driven a point ahead of the next open stop is added
    Given a trip whose document carries no routing points
    And the trip-keeper is signed in with the plan saved
    And the car has arrived at "Alder"
    When the trip-keeper adds the routing point "Pine Fork" to day 1 after stop 2 on the ridge road
    Then the routing point is added
    And the trip has 5 stops
    When the car sits just after Alder on the ridge road
    Then Navigate in Waze goes to "Birch Gate"
    When the car sits past Birch Gate
    Then Navigate in Waze goes to "Pine Fork"

  Scenario: On the leg being driven a point between the car and the next open stop is the next target
    Given a trip whose document carries no routing points
    And the trip-keeper is signed in with the plan saved
    And the car has arrived at "Alder"
    When the trip-keeper adds the routing point "Pine Fork" to day 1 after stop 1 on the ridge road
    Then the routing point is added
    When the car sits just after Alder on the ridge road
    Then Navigate in Waze goes to "Pine Fork"

  Scenario: A point behind a stop the car is already past is refused and nothing changes
    Given a trip whose document carries no routing points
    And the trip-keeper is signed in with the plan saved
    And the car has arrived at "Alder"
    And the car has arrived at "Birch Gate"
    When the trip-keeper adds the routing point "Pine Fork" to day 1 after stop 1 on the ridge road
    Then the routing point is refused
    And day 1 holds no routing point
    And the trip has 5 stops

  Scenario: A leg already finished takes no routing point
    Given a trip whose document carries no routing points
    And the trip-keeper is signed in with the plan saved
    And the car has arrived at "Alder"
    And the car has arrived at "Birch Gate"
    And the car has arrived at "Cedar Camp"
    When the trip-keeper adds the routing point "Pine Fork" to day 1 after stop 2 on the ridge road
    Then the routing point is refused
    And day 1 holds no routing point

  Scenario: A point added on the road to a driven leg does not open the document's points to a rewrite
    Given a trip whose document carries routing points
    And the car has arrived at "Alder"
    When the trip-keeper adds the routing point "Pine Fork" to day 1 after stop 2 on the ridge road
    Then the routing point is added
    When the document is pushed again with "Plum Bridge" renamed
    Then the push is refused whole

  Scenario: Pushing the document again cannot rewrite the routing points of a leg already driven
    Given a trip whose document carries routing points
    And the car has arrived at "Birch Gate"
    When the document is pushed again with "Plum Bridge" renamed
    Then the push is refused whole

  Scenario: A routing point at a private place is refused
    Given a trip whose document carries no routing points
    When a document is pushed with a routing point at the private start
    Then the push is not accepted
