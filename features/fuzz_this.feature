Feature: Fuzz This, and how far a precise location travels
  Kris, 2026-11-08: precise location is allowed on a photo a signed-in driver
  posts, and during and after any upload one easy "Fuzz This" button makes that
  one post's location fuzzy. Being signed in is what recognizes the driver: no
  new key and no device token. The phone's own location prompt is the
  permission.
  Ruled the same day: Claude may be told a post's exact location, except when
  the post is fuzzed or it lies near a stop marked private. Fuzz keeps the exact
  spot privately so Unfuzz can restore it. The exact spot is shown to no one
  and sent nowhere while the post is fuzzed. Photos themselves still never go
  to Chester.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at

  Scenario: The post form offers Fuzz This before the post is made
    When kris opens the post form
    Then the form offers "Fuzz This"

  Scenario: Fuzz This at upload publishes a fuzzy location
    Given a photo with its GPS 30 km from every stop
    When kris posts that photo with "Fuzz This" on
    Then the post's published location is fuzzy
    And the post's published location is not the photo's own GPS

  Scenario: A post left alone keeps its exact location
    Given a photo with its GPS 30 km from every stop
    When kris posts that photo
    Then the post's published location is the photo's own GPS

  Scenario: The notice after posting offers Fuzz This for that post
    Given a photo with its GPS 30 km from every stop
    When kris posts that photo
    Then the page after posting offers "Fuzz This" for that post

  Scenario: One tap fuzzes a post after it is posted
    Given a post pinned exactly 30 km from every stop
    When kris taps "Fuzz This" on that post
    Then the post's published location is fuzzy
    And the post card offers "Unfuzz"
    And the bus carries a post_fuzzed record with no latitude and no longitude

  Scenario: Unfuzz restores the exact location
    Given a post pinned exactly 30 km from every stop
    And kris has tapped "Fuzz This" on that post
    When kris taps "Unfuzz" on that post
    Then the post's published location is the exact point it had before
    And the post card offers "Fuzz This"

  Scenario: Fuzz This at upload can be undone too
    Given a photo with its GPS 30 km from every stop
    And kris has posted that photo with "Fuzz This" on
    When kris taps "Unfuzz" on that post
    Then the post's published location is the photo's own GPS

  Scenario: The exact spot of a fuzzed post appears nowhere a reader can get it
    Given a post pinned exactly 30 km from every stop
    And kris has tapped "Fuzz This" on that post
    When the home page, the post's card, the route data and the posts census are read
    Then none of them contains the exact point

  Scenario: Only a signed-in driver can fuzz or unfuzz
    Given a post pinned exactly 30 km from every stop
    When a visitor who is not signed in tries to fuzz that post
    Then the post's published location is still the exact point
    And a visitor sees no "Fuzz This" on the post card

  Scenario: A post with no location has nothing to fuzz
    Given a post with no location
    When kris opens the page with that post
    Then that post offers no "Fuzz This"

  Scenario: Re-pinning a fuzzed post starts from the new pin
    Given a post pinned exactly 30 km from every stop
    And kris has tapped "Fuzz This" on that post
    When kris re-pins that post to no pin
    Then that post offers no "Unfuzz"

  Scenario: Claude is told a post's exact location
    Given a post pinned exactly 30 km from every stop
    When Chester's context is built
    Then it carries the post's exact location

  Scenario: Claude is told only the fuzzy location of a fuzzed post
    Given a post pinned exactly 30 km from every stop
    And kris has tapped "Fuzz This" on that post
    When Chester's context is built
    Then it carries the fuzzy location and not the exact point

  Scenario: Claude is never told an exact location near a private stop
    Given stop B is a private place
    And a post pinned exactly at B with fuzz turned off
    When Chester's context is built
    Then it carries B's fuzzed cell and not the exact point

  Scenario: The car's live position stays coarse for Claude
    Given the phone reports an exact position 30 km from every stop
    When Chester's context is built from that position
    Then it carries the position rounded to three decimals
