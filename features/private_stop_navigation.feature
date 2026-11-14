Feature: The signed-in trip-keeper navigates to a private stop's own address
  (Kris, 2026-11-12.) A friend's address may be in the database, marked
  private: a visitor never sees it and the stop is fuzzed for them. For the
  signed-in trip-keeper the database and the navigation are precise. When the
  stop being navigated to is private and has an address on the volume's private
  overlay, the Waze, Google Maps and Apple Maps links go to that address, as
  search text. A private stop with no address navigates as before, by its
  name. A precise stop still navigates by its coordinates. Every navigate link
  is built in one place. Nothing here reaches a visitor: no page and no JSON
  they can read carries the address. Every place and address here is invented.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at
    And the drive has started at A

  Scenario: The driver screen's links go to the private address
    Given B is a private place
    And B has the private address "7 Quill Hollow Lane, Pemberwick"
    When kris reads the driver screen's navigation
    Then the driver links for Waze, Google Maps and Apple Maps carry the address of B

  Scenario: The Ride page's Navigate buttons go to the private address
    Given B is a private place
    And B has the private address "7 Quill Hollow Lane, Pemberwick"
    When kris reads the Ride page's navigation
    Then the Ride row for B has Waze, Google Maps and Apple Maps links that carry the address of B

  Scenario: The day's end links go to the private address
    Given D is a private place
    And D has the private address "7 Quill Hollow Lane, Pemberwick"
    When kris reads the Ride page's navigation
    Then the day's end links carry the address of D

  Scenario: A private stop with no address navigates as today
    Given B is a private place
    When kris reads the driver screen's navigation
    Then the driver links go to B by its name and carry no coordinates

  Scenario: Signed out, no page and no JSON carries the address
    Given B is a private place
    And B has the private address "7 Quill Hollow Lane, Pemberwick"
    When a visitor reads every page and JSON the site offers
    Then none of them carries the address of B

  Scenario: A precise stop is unchanged
    Given B has the private address "7 Quill Hollow Lane, Pemberwick"
    When kris reads the driver screen's navigation
    Then the driver links go to B by its coordinates

  # Kris, 2026-11-13. A route choice can put a different stop under the same
  # number (a visit on the way pushes the finish from stop 7 to stop 8 and takes
  # number 7 itself). An address may therefore say which stop it was filed for;
  # it is shown only against a stop of that name. An entry with no name behaves as
  # before, by its number alone.
  Scenario: An address filed for a named stop shows against that stop
    Given B is a private place
    And B has the private address "7 Quill Hollow Lane, Pemberwick" filed for its own name
    When kris reads the driver screen's navigation
    Then the driver links for Waze, Google Maps and Apple Maps carry the address of B

  Scenario: An address filed for another stop does not show against the stop that took its number
    Given B is a private place
    And B has the private address "7 Quill Hollow Lane, Pemberwick" filed for the stop "Elsewhere Corners"
    When kris reads the driver screen's navigation
    Then the driver links go to B by its name and carry no coordinates

  Scenario: An address filed for another stop is not on the post form or the Lodging screen, and a Lodging save keeps it
    Given B is a private place
    And B has the private address "7 Quill Hollow Lane, Pemberwick" filed for the stop "Elsewhere Corners"
    When kris opens the post form and the Lodging screen
    Then neither shows the address
    When kris saves the Lodging screen as it stands
    Then the overlay still holds the address filed for "Elsewhere Corners"
