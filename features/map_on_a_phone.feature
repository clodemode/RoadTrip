Feature: The map on a phone
  The maps (the Ride, a leg and the Plan) are used one-handed from a phone.
  One finger scrolls the page, and two fingers move the map. Photos and music
  notes that would sit on top of each other gather into one round count, and a
  tap on the count opens them up. The map is taller than before and can fill the
  screen in place, with no dialog. Every map control is a 44 pixel target, and a
  button puts the map back on the whole trip. A leg's map opens on that leg.

  Scenario: One finger scrolls the page and does not move the map
    Given the Ride map is opened on a phone
    When one finger moves on the map
    Then the map is not set to pan under a single finger
    And a hint says "Use two fingers to move the map"
    And the hint is gone two seconds later

  Scenario: Two fingers move and zoom the map
    Given the Ride map is opened on a phone
    When two fingers move on the map
    Then moving and pinching with two fingers stay on
    And no hint is shown

  Scenario: A mouse still drags the map on a desktop
    Given the Ride map is opened with a mouse
    Then dragging with the mouse is on
    And the mouse wheel does not zoom the map

  Scenario: Photos and music notes that overlap gather into a count and spread when tapped
    Given the Ride map is opened with 3 photos and a music note within 200 metres of one stop
    Then the 3 photos and the music note gather into one count of 4
    And the count is a round badge with the number 4
    And the stop's pins and the day pin are never gathered
    When the visitor taps the count
    Then the pins spread into more than one group

  Scenario: The map is taller on a phone and Full screen fills the window in place
    Given a phone 375 pixels wide and 812 tall
    Then the map is taller than 380 pixels
    And Full screen is a class on the map with the window fixed and no dialog
    When the Ride map is opened on a phone
    And the visitor presses Full screen
    Then the map fills the window in place
    And a Done button is shown
    When the visitor presses Done
    Then the map is back in the page
    And a Done button is not shown

  Scenario: Escape leaves full screen
    Given the Ride map is opened on a phone
    When the visitor presses Full screen
    Then the map fills the window in place
    When the visitor presses the Escape key
    Then the map is back in the page

  Scenario: Every map control is a 44 pixel target
    Then the zoom buttons and the layers button are at least 44 by 44 pixels
    And the Fit and Full screen and Done buttons are at least 44 by 44 pixels
    And the Plan map takes the same controls as the Ride map
    And full screen sits above the Listen Inn card

  Scenario: Fit trip puts the map back on the opening view
    Given the Ride map is opened on a phone
    Then the Ride map offers a Fit trip button
    When the visitor moves the map and presses Fit trip
    Then the view is the one the page opened with

  Scenario: The leg map fits the leg and not a stray pin
    Given the leg map of Winchester to Nashville with a post pinned far from the road
    Then the leg's line and its two checkpoints are inside the opening view
    And the leg map offers a Fit leg button
    And the stray post does not widen the opening view
