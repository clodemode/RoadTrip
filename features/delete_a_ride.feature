Feature: The trip-keeper can delete a ride nothing has happened on
  A trip pushed by mistake, or one that was only ever planned, has no way off
  the site. POST /driver/ride/{slug}/delete removes it: its rows, its trip
  file on the volume and its block in the private overlay. A ride that is
  showing, that ships with the site, or that anything has happened on is
  refused whole, so a delete can never cost a post, a photo or an arrival.

  Background:
    Given a site whose active trip is pushed
    And a second pushed trip with a private block
    And kris is the signed-in trip-keeper

  Scenario: A planned ride nothing has happened on is deleted
    When kris deletes the second trip
    Then the delete is answered 200 saying the second trip is deleted
    And the second trip is gone from the database, the volume and the private overlay
    And the active trip is untouched

  Scenario: The active ride is refused
    When kris deletes the active trip
    Then the delete is answered with 409
    And the active trip is untouched

  Scenario: A ride with an arrived checkpoint is refused
    Given a checkpoint of the second trip is arrived at
    When kris deletes the second trip
    Then the delete is answered with 409
    And the second trip is still whole

  Scenario: A ride with a post is refused
    Given a post on the second trip
    When kris deletes the second trip
    Then the delete is answered with 409
    And the second trip is still whole

  Scenario: A ride that ships with the site is refused
    When kris deletes the ride that ships with the site
    Then the delete is answered with 409

  Scenario: A ride that is not there is a 404
    When kris deletes a ride called "no-such-ride"
    Then the delete is answered with 404

  Scenario: A slug that is not a slug is a 400
    When kris deletes a ride called "Not_A_Slug"
    Then the delete is answered with 400

  Scenario: Only the trip-keeper may delete
    Given ann is a signed-in editor
    When ann deletes the second trip
    Then the delete is answered with 403
    And the second trip is still whole
