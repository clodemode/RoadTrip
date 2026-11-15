Feature: Waze drops a routing point the car has passed
  The Ride page's Navigate links go to the first routing point still ahead. A
  routing point used to be passed only once a LATER STOP was arrived at, so on
  the day's last stretch, where the only stop after it is the day's end, driving
  past it changed nothing and the link kept sending the car back (day 4, 2026-11-14).
  The page now also reads the car's furthest position the app already holds (a
  post's pin, a Chester ask, a track play: each already published, never a raw
  point). A point is passed once that position is at least 300 m beyond it along
  the day's line. Every place and coordinate here is invented.

  Scenario: A routing point ahead of the car stays in the link
    Given a trip whose document carries routing points
    And the car has arrived at "Alder"
    And the app holds the car just after Alder on the ridge road
    When the driver reads the Ride page
    Then the Ride page Waze link goes to "Plum Bridge"

  Scenario: A routing point the car has passed drops out before any later stop is arrived
    Given a trip whose document carries routing points
    And the car has arrived at "Alder"
    And the app holds the car past Plum Bridge
    When the driver reads the Ride page
    Then the Ride page Waze link goes to "Quince Turn"
    And the Ride page does not name "Plum Bridge"

  Scenario: Past the last routing point the link goes to the day's end
    Given a trip whose document carries routing points
    And the car has arrived at "Alder"
    And the app holds the car past Quince Turn
    When the driver reads the Ride page
    Then the Ride page Waze link goes to "Cedar Camp"
    And the Ride page does not name "Quince Turn"

  Scenario: A passed point passed only by a noisy fix behind the car stays passed
    Given a trip whose document carries routing points
    And the car has arrived at "Alder"
    And the app holds the car past Plum Bridge
    And the app holds the car just short of Plum Bridge
    When the driver reads the Ride page
    Then the Ride page Waze link goes to "Quince Turn"

  Scenario: GPS jitter at the point does not drop it early
    Given a trip whose document carries routing points
    And the car has arrived at "Alder"
    And the app holds the car just short of Plum Bridge
    And the app holds the car a few metres past Plum Bridge
    When the driver reads the Ride page
    Then the Ride page Waze link goes to "Plum Bridge"

  Scenario: A fix far off the day's road drops nothing
    Given a trip whose document carries routing points
    And the car has arrived at "Alder"
    And the app holds the car far off the road beyond Quince Turn
    When the driver reads the Ride page
    Then the Ride page Waze link goes to "Plum Bridge"

  Scenario: On a loop the car just leaving the start has passed nothing
    Given a trip whose document is a loop that ends where it began
    And the car has arrived at "Mill Start"
    And the app holds the car just after the start of the loop
    When the driver reads the Ride page
    Then the Ride page Waze link goes to "Mill Corner"

  Scenario: On a loop the car past the corner has passed it and the finish is unaffected
    Given a trip whose document is a loop that ends where it began
    And the car has arrived at "Mill Start"
    And the app holds the car past Mill Corner on the loop
    When the driver reads the Ride page
    Then the Ride page Waze link goes to "Mill Home"
    And the Ride page offers the day's end "Mill Home"

  Scenario: A position at a private place is never shown raw
    Given a trip whose document carries routing points
    And the car has arrived at "Alder"
    And the phone sends a position from the private start's real door
    When the driver reads the Ride page
    Then the Ride page Waze link goes to "Plum Bridge"
    And the Ride page shows no coordinate of the private start's real door
