Feature: Route me here on a Chester place card
  Chester names a place; the trip-keeper taps Route me here and the driver
  screen goes to that place as a detour. The plan is not touched, the model is
  not told where the car then is, and the way back to the plan is the Trip
  button. The card carries the place as text; the server looks it up.

  Scenario: A place card carries Route me here
    Given Chester will name the place "Larch Diner" in "Dogwood, ZZ"
    When the trip-keeper asks Chester for a place to eat
    Then the places event carries the route "Larch Diner, Dogwood, ZZ"
    And the page builds Route me here as a form posting to the freelance destination

  Scenario: Route me here sets the destination and leaves the plan unchanged
    Given a trip-keeper signed in with the plan saved
    When the trip-keeper taps Route me here for "Hudson Bend Coffee, Hudson Bend, TX"
    Then the driver is in Destined mode with a destination set
    And the browser is sent back to the driver screen
    And the plan is unchanged

  Scenario: A place TomTom cannot find leaves the driver told so
    Given a trip-keeper signed in with the plan saved
    And TomTom knows no such place
    When the trip-keeper taps Route me here for "Nowhere Cafe, Nowhere, ZZ"
    Then the driver is told "found no place"
    And no destination is set
    And the browser is sent back to the driver screen

  Scenario: The driver screen says a detour is in force and offers Trip
    Given a trip-keeper signed in with the plan saved
    When the trip-keeper taps Route me here for "Hudson Bend Coffee, Hudson Bend, TX"
    Then the driver screen says "Detour in force"
    And the driver screen offers Trip

  Scenario: Nothing of it reaches the model
    Given Chester will name the place "Larch Diner" in "Dogwood, ZZ"
    And a trip-keeper signed in with the plan saved
    When the trip-keeper taps Route me here for "Hudson Bend Coffee, Hudson Bend, TX"
    And the trip-keeper asks Chester for a place to eat
    Then the model's requests hold no TomTom coordinate
