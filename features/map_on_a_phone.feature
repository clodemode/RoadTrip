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
    And every photo and note is in the gathering group
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
    Then the zoom buttons and the Music, Photos and Notes buttons are at least 44 by 44 pixels
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

  # Ruling 2 (Kris 2026-11-16): simple controls to show Music, Photos and Notes;
  # the route stays; the day and waypoint markers go from the Ride and leg maps.

  Scenario: The Ride map has Music, Photos and Notes buttons, all on
    Given the Ride map is opened on a phone
    Then the map has buttons labelled "Music", "Photos" and "Notes"
    And each of them is pressed on
    And the map has no layers control

  Scenario: Turning Photos off hides the photo pins and their counts, and Notes and Music stay
    Given the Ride map is opened with 3 photos, 2 notes and a music note within 200 metres of one stop
    Then the pins gather into one count of 6
    When the visitor turns Photos off
    Then the pins gather into one count of 3
    And no photo pin is on the map
    And the Photos button is not pressed and the Notes and Music buttons are pressed
    When the visitor turns Photos on
    Then the pins gather into one count of 6

  Scenario: Turning Music off removes the music notes and Notes off removes the notes
    Given the Ride map is opened with 3 photos, 2 notes and a music note within 200 metres of one stop
    When the visitor turns Music off
    Then the pins gather into one count of 5
    When the visitor turns Notes off
    Then the pins gather into one count of 3
    And no note pin and no music pin is on the map

  Scenario: The route has no button and stays when Music, Photos and Notes are all off
    Given the Ride map is opened with 3 photos, 2 notes and a music note within 200 metres of one stop
    When the visitor turns Music, Photos and Notes off
    Then no pin is on the map
    And the planned leg lines are still drawn
    And the driven track is still drawn
    And the map has no button for the route

  Scenario: The Ride map draws no day, start, checkpoint, sight or hours markers
    Given the Ride map is opened on a phone
    Then no day pin, start dot, checkpoint pin, candidate stop or hours stick is drawn
    And the Ride map still fits the route

  Scenario: The leg map draws none of them either and fits the route
    Given the leg map of Winchester to Nashville with a post pinned far from the road
    Then no day pin, start dot, checkpoint pin, candidate stop or hours stick is drawn
    And the leg map still opens on the leg's line

  Scenario: The Plan map keeps its stop pins and does not get the toggles
    Then the Plan map still draws its stop pins
    And the Plan map has no Music, Photos or Notes buttons

  Scenario: No map control sits where the docked Listen Inn covers it
    Given a phone 375 pixels wide and 812 tall
    And the Ride map is opened on a phone
    Then the Music, Photos and Notes and the Fit and Full screen buttons are in the top corners of the map
    And the zoom buttons are in the top left corner of the map
    And the Listen Inn card is fixed to the bottom of the window
