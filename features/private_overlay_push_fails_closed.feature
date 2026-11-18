Feature: The private overlay push fails closed
  (Kris, 2026-11-18.) An overlay entry whose "for" equals no stop name at its
  number was dropped in silence, and that is how an address could be on file
  and still not be used. A push that holds such an entry is refused whole, by
  name, while the pusher is watching; the refusal names the entry number and the
  "for" text it could not match, and never an address. The push goes to a ride's
  path, because URLs are REST paths and never query parameters. Every place and
  address here is invented.

  Background:
    Given the trip-keeper pushes with the push token and is signed in
    And a trip document whose stops carry no address and no navigate mark
    And the trip is pushed

  Scenario: An entry filed for a name no stop carries is refused and the answer names it
    When the overlay is pushed by ride path with the entry 2 for "Elsewhere Corners" at "9 Larkspur Row, Pemberwick"
    Then the trip-keeper's push is refused with a 400
    And the answer names entry 2 and "Elsewhere Corners"
    And the overlay on the volume is as it was

  Scenario: An entry under a number no stop holds is refused
    When the overlay is pushed by ride path with the entry 9 for "" at "9 Larkspur Row, Pemberwick"
    Then the trip-keeper's push is refused with a 400
    And the answer names entry 9
    And the overlay on the volume is as it was

  Scenario: The refusal never prints an address
    When the overlay is pushed by ride path with the entry 9 for "Elsewhere Corners" at "9 Larkspur Row, Pemberwick"
    Then the trip-keeper's push is refused with a 400
    And the answer does not carry "9 Larkspur Row, Pemberwick"

  Scenario: A push by ride path replaces that ride's block and no other
    Given the first ride's block holds an entry at "4 Tamarack Close, Fenwick"
    When the overlay is pushed by ride path with the entry 2 for "Birch Gate" at "9 Larkspur Row, Pemberwick"
    Then the trip-keeper's push is taken
    And the trip's block holds only the address "9 Larkspur Row, Pemberwick" at 2
    And the first ride's block still holds "4 Tamarack Close, Fenwick"

  Scenario: A push with a ride query parameter is refused and names the path
    When the overlay is pushed with the ride query parameter
    Then the trip-keeper's push is refused with a 400
    And the answer names the path "/driver/private/ride/"
