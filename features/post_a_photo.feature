Feature: Post a Photo
  The post form is a photo first. A stop is never chosen on it: a photo
  posted at the next stop checks in there (see start_and_checkin.feature),
  and that scores the stop's ETA predictions.

  Background:
    Given kris is signed in
    And the active trip has milestones A, B and C in order, with A already arrived at

  Scenario: The form is a photo first
    When kris opens the post form
    Then its heading is "Post a Photo"
    And no milestone is selected
    And the form carries no arrival button

  Scenario: Posting with no milestone marks nothing
    When kris posts a photo with no milestone chosen
    Then the post exists
    And B and C are still not arrived at

  Scenario: Marking a milestone from a photo scores its ETA predictions
    Given an ETA prediction for B exists
    When kris posts a photo taken at B
    Then that prediction is scored
    And the bus carries an eta_hit record for B with no latitude and no longitude
