Feature: The privacy seams hold at a private place and after a delete
  Audit, 2026-11-16. Per-post choice is ruled: a driver may publish an exact
  pin. At a private place that takes a second, separate yes, because one
  un-ticked box used to publish the door. A deleted post's photo comes off the
  public path unless another post still shows it.

  Scenario: Fuzz off at a private place still publishes the cell without a second yes
    Given a private stop and a phone standing at its door
    When a post is pinned there with the fuzz turned off
    Then the published pin is the private stop's cell, not the door
    When a post is pinned there with the fuzz turned off and the exact spot confirmed
    Then the published pin is the door

  Scenario: Fuzz off away from any private place needs no second yes
    Given a private stop and a phone standing 30 km from it
    When a post is pinned there with the fuzz turned off
    Then the published pin is where the phone stood

  Scenario: A deleted post's photo comes off the public path
    Given a photo post whose photo is served on the public media path
    When the trip-keeper deletes that post
    Then the photo is no longer served
    And the thumbnail is no longer served

  Scenario: A photo another post still shows stays when one of the posts is deleted
    Given two posts that show the same photo
    When the trip-keeper deletes the first of them
    Then the photo is still served

  Scenario: The drivers' route data is never kept by a shared cache
    When the trip-keeper asks for the route data
    Then the answer is marked private and not to be stored

  Scenario: A missing media file is not cached for a year
    When a visitor asks for a media file that is not there
    Then the answer carries no long-lived cache header
