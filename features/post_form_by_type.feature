Feature: The post form opens on what is being posted
  Kris, 2026-11-08: "We tried to Start Drive, and it wanted a photo. When I
  provided a photo, it asked what type of post. Make the form smarter and flow
  using collapse/expand divs based on post type." The type comes first, as
  large choices, Photo by default; the sections below expand or collapse to
  suit it. Before the drive starts, in Trip mode, Start the drive lands on the
  same address but opens straight on the photo. In FreeLance Aimless or
  Destined there is no start to make, so the form never offers one.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at

  Scenario: Photo is the default type and every kind is a choice
    Given the drive has started at A
    When kris opens the post form
    Then the form has a "kind" radio for every post kind
    And the photo kind is the one selected

  Scenario: Before the start, in Trip mode, the form opens in start mode
    When kris opens the post form
    Then the form is in start mode for A
    And the form offers "Post something else instead"

  Scenario: In Aimless mode the form offers no start
    Given kris is in "aimless" mode
    When kris opens the post form
    Then the form offers no start
    And its heading is "Post a Photo"
    And the driver screen's photo tool reads "Post photo"

  Scenario: In Destined mode the form offers no start
    Given kris is in "destined" mode
    When kris opens the post form
    Then the form offers no start
    And the driver screen's photo tool reads "Post photo"

  Scenario: After the start the normal form shows
    Given the drive has started at A
    When kris opens the post form
    Then its heading is "Post a Photo"
    And the form does not offer to start the drive
    And the form does not offer "Post something else instead"

  Scenario: The plumbing is tucked into More options
    Given the drive has started at A
    When kris opens the post form
    Then More options holds Leg, Zone, Checkpoint, When and Pin
    And "Fuzz This" stays outside More options

  Scenario: A Start with a hand-opened full form is still the same form
    When kris asks for the full form
    Then the form offers "Start the drive from A"
    And the form offers "Post a Photo"

  Scenario: A posted Note still needs no photo
    Given the drive has started at A
    When kris posts a note with no photo
    Then the post exists

  Scenario: A Start with no photo is still refused
    When kris starts the drive with no photo
    Then the request is refused
    And A is still not arrived at
