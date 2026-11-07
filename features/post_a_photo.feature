Feature: Post a Photo
  The post form is a photo first. Marking a milestone is an option the
  driver takes on purpose, from the milestones not yet reached, and the
  photo's own time becomes the arrival time.

  Background:
    Given kris is signed in
    And the active trip has milestones A, B and C in order, with A already arrived at

  Scenario: The form is a photo first
    When kris opens the post form
    Then its heading is "Post a Photo"
    And no milestone is selected
    And the form carries no arrival button

  Scenario: Only milestones not yet arrived at are offered
    When kris opens the post form
    Then the milestone choices are B and C
    And A is not among them

  Scenario: Posting with no milestone marks nothing
    When kris posts a photo with no milestone chosen
    Then the post exists
    And B and C are still not arrived at

  Scenario: Posting with a milestone marks it arrived at the photo's time
    Given a photo taken at 10:42 in B's zone
    When kris posts that photo with milestone B chosen
    Then B is arrived at 10:42 in B's zone
    And the post exists

  Scenario: A photo without a time marks the milestone arrived now
    Given a photo with no time of its own
    When kris posts that photo with milestone C chosen
    Then C is arrived at the current time

  Scenario: A milestone already arrived at cannot be marked again from the form
    When kris posts a photo with milestone A chosen
    Then the request is refused
    And A's arrival time is unchanged

  Scenario: Marking a milestone from a photo scores its ETA predictions
    Given an ETA prediction for B exists
    When kris posts a photo with milestone B chosen
    Then that prediction is scored
    And the bus carries an eta_hit record for B with no latitude and no longitude

  Scenario: The form tells the page where each offered milestone is
    When kris opens the post form
    Then each offered milestone carries its published position as data attributes
    And a milestone that is a private place carries its snapped cell, never its point
