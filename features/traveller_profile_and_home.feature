Feature: Each traveller keeps a private Home, a trip can start or finish at home, and every ride has an owner
  Kris, 2026-11-18: the site should hold Home like Waze does, and a lot of
  trips start and finish there. Ruled 2026-11-26: Home's point is where the
  phone stands when its owner taps Set Home, and any signed-in traveller may
  own a ride. Home is private: its address is shown to its owner and used to
  navigate, its point reaches the map only fuzzed, and it is in no public
  page or JSON. The Home used here is invented.

  Background:
    Given a site where kris is an admin and lee is a traveller

  Scenario: A signed-in traveller saves a Home address and sees it on their profile
    When kris saves Home from the phone at the invented Home with a 20 m fix
    Then kris's profile page shows the invented Home address
    And kris's profile page says Home's point is set

  Scenario: A phone fix that is not close enough does not set Home
    When kris saves Home from the phone at the invented Home with a 450 m fix
    Then the profile answers 400 and says the fix is not close enough
    And kris's profile page says Home has no point yet

  Scenario: A visitor, and another signed-in traveller, cannot read it
    Given kris has saved the invented Home
    When lee opens the profile page
    Then lee's profile page does not show the invented Home address
    And a visitor who opens the profile page is sent to sign in

  Scenario: Only the trip-keeper's machine path names a traveller's profile
    When lee puts a profile for kris
    Then the profile put answers 403
    When the push token puts a profile with a display name for kris
    Then the profile put answers 200 and names no address

  Scenario: A trip whose start and finish say home takes Home's point, fuzzed, and its label
    Given kris has saved the invented Home
    When the home loop trip is pushed and made active
    Then the trip's start and finish are named "Home"
    And both sit on the fuzzed cell of the invented Home
    And neither the trip file nor any public page carries the invented Home

  Scenario: Navigate at the finish goes to the owner's Home address
    Given kris has saved the invented Home
    When the home loop trip is pushed and made active
    Then kris's readiness page sends the finish to the invented Home address
    And a visitor's ride page does not carry the invented Home address

  Scenario: A trip that says home when its owner has saved none is refused and names the place
    When the home loop trip is pushed
    Then the push answers 400 naming the place "home"

  Scenario: A ride pushed with no owner belongs to the first admin
    Given kris has saved the invented Home
    When the home loop trip is pushed and made active
    Then the ride's owner is kris

  Scenario: The cast line shows the owner's display name
    Given kris has saved the invented Home
    And kris's display name is "Kris the Keeper"
    When the home loop trip is pushed and made active
    Then the ride page's cast line reads "Trip-keeper: Kris the Keeper"
