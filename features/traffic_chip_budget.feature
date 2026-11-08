Feature: The Traffic chips on a trip leg are a short list of what is ahead
  A driver reads the chips at a glance. TomTom answers a wide box around each
  sample point, so most incidents are on side roads nowhere near the route.
  On a planned leg an incident gets a chip only when it lies on the leg's
  line and ahead of the car; the nearest three show, with how far, and the
  rest are one summary chip. An incident with no position is never hidden.
  Aimless and Destined have no leg line and keep their chips and cap.

  Background:
    Given a leg 100 miles long running east

  Scenario: 150 incidents of which 5 lie on the line give three chips and a summary
    Given TomTom reports 145 closures far from the leg
    And TomTom reports a closure "Quarry Rd to Mill Rd" at mile 31
    And TomTom reports a closure "Bellrock Rd to German Rd" at mile 12
    And TomTom reports a delay "Pine St to Oak St" at mile 25
    And TomTom reports a delay "Birch St to Ash St" at mile 80
    And TomTom reports a closure "Elm St to Fir St" at mile 52
    When the Trip hazards are asked for
    Then the traffic chips read:
      """
      items|Traffic: 12 mi ahead, Road closed, Bellrock Rd to German Rd
      items|Traffic: 25 mi ahead, Stationary traffic, Pine St to Oak St
      items|Traffic: 31 mi ahead, Road closed, Quarry Rd to Mill Rd
      items|Traffic: 2 more ahead, next at 52 mi
      """

  Scenario: A nearer delay comes before a farther closure
    Given TomTom reports a closure "Elm St to Fir St" at mile 40
    And TomTom reports a delay "Pine St to Oak St" at mile 8
    When the Trip hazards are asked for
    Then the traffic chips read:
      """
      items|Traffic: 8 mi ahead, Stationary traffic, Pine St to Oak St
      items|Traffic: 40 mi ahead, Road closed, Elm St to Fir St
      """

  Scenario: Closures off the route alone read as nothing on the route
    Given TomTom reports 6 closures far from the leg
    When the Trip hazards are asked for
    Then the traffic chips read:
      """
      clear|Traffic: nothing on the route (6 nearby off route)
      """

  Scenario: An incident behind the car shows no chip
    Given TomTom reports a closure "Elm St to Fir St" at mile 10
    And TomTom reports a closure "Quarry Rd to Mill Rd" at mile 40
    And the car is at mile 20
    When the Trip hazards are asked for
    Then the traffic chips read:
      """
      items|Traffic: 20 mi ahead, Road closed, Quarry Rd to Mill Rd
      """

  Scenario: With everything behind the car nothing is ahead
    Given TomTom reports a closure "Elm St to Fir St" at mile 10
    And the car is at mile 20
    When the Trip hazards are asked for
    Then the traffic chips read:
      """
      clear|Traffic: nothing ahead on the route
      """

  Scenario: With no position the distance runs from the start of the leg
    Given TomTom reports a closure "Elm St to Fir St" at mile 12
    And the car has no position
    When the Trip hazards are asked for
    Then the traffic chips read:
      """
      items|Traffic: 12 mi ahead, Road closed, Elm St to Fir St
      """

  Scenario: A closure drawn as a line that covers the car is at the car
    Given TomTom reports a closure "Elm St to Fir St" from mile 30 to mile 40
    And the car is at mile 35
    When the Trip hazards are asked for
    Then the traffic chips read:
      """
      items|Traffic: 0 mi ahead, Road closed, Elm St to Fir St
      """

  Scenario: An incident with no geometry still shows, after the measured ones
    Given TomTom reports a closure "Mystery Rd to Hidden Rd" with no geometry
    And TomTom reports a closure "Elm St to Fir St" at mile 30
    When the Trip hazards are asked for
    Then the traffic chips read:
      """
      items|Traffic: 30 mi ahead, Road closed, Elm St to Fir St
      items|Traffic: Road closed, Mystery Rd to Hidden Rd
      """

  Scenario: The request asks TomTom for each incident's geometry
    Given TomTom reports a closure "Elm St to Fir St" at mile 30
    When the Trip hazards are asked for
    Then TomTom was asked for geometry

  Scenario: A TomTom failure reads unknown
    Given TomTom answers 500
    When the Trip hazards are asked for
    Then the traffic chips read:
      """
      unknown|Traffic: unknown (error)
      """
    And the TomTom key appears in no chip

  Scenario: The chips follow the car inside the cache window without asking TomTom again
    Given TomTom reports a closure "Bellrock Rd to German Rd" at mile 12
    And TomTom reports a closure "Elm St to Fir St" at mile 52
    And the car is at mile 0
    When the Trip hazards are asked for
    Then the traffic chips read:
      """
      items|Traffic: 12 mi ahead, Road closed, Bellrock Rd to German Rd
      items|Traffic: 52 mi ahead, Road closed, Elm St to Fir St
      """
    When the car moves to mile 20 and the Trip hazards are asked for again
    Then the traffic chips read:
      """
      items|Traffic: 32 mi ahead, Road closed, Elm St to Fir St
      """
    And TomTom was not asked again

  Scenario: Aimless and Destined keep their chips and their cap
    Given TomTom reports 6 closures far from the leg
    When the hazards are asked for without a leg line
    Then the traffic chips read:
      """
      items|Traffic: Road closed, Side Rd 1 to Lane 1
      items|Traffic: Road closed, Side Rd 2 to Lane 2
      items|Traffic: Road closed, Side Rd 3 to Lane 3
      items|Traffic: Road closed, Side Rd 4 to Lane 4
      items|Traffic: and 2 more
      """
    And TomTom was not asked for geometry
