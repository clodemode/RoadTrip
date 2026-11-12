Feature: One tap posts
  Kris, 2026-11-12: "The Post feature should take FAR LESS clicks. Should be -
  click a button to match the post type, as soon as it is uploaded, it is
  posted. If a title or note is desired, it needs to be entered before the
  post." Yesterday's fifteen posts each took a page open, 16 to 20 seconds of
  form work and a Post tap. Now the page opens on an optional title, an
  optional note and one big button per post type. A button for a type that
  carries a photo is the file picker: the photo is posted the moment it is
  picked. A note posts on the tap. The rest of the form (zone, leg, stop,
  time, pin, Fuzz This) is under More options, and a post the driver wants to
  make carefully still has a visible Post button there. Where the phone puts
  the pin, and what a private place does to it, are decided as before.

  Background:
    Given kris is signed in
    And the active trip has stops A, B, C and D in order along a line, none arrived at

  Scenario: The page offers a title, a note and one button per type
    Given the drive has started at A
    When kris opens the post form
    Then the form has a button for every post kind
    And an optional title and an optional note sit above the buttons
    And the page says to type a title or note first, then tap what is being posted

  Scenario: A photo type posts with only its kind and the file
    Given the drive has started at A
    When kris taps "Sight" and picks a photo with nothing typed
    Then the post exists
    And the post is a "sight"

  Scenario: A title typed first is saved on that post
    Given the drive has started at A
    When kris types the title "Lunch at the diner" and taps "Meal" and picks a photo
    Then the post exists
    And the post's title is "Lunch at the diner"

  Scenario: A note typed first is saved on that post
    Given the drive has started at A
    When kris types the note "Pie was good." and taps "Meal" and picks a photo
    Then the post exists
    And the post's note is "Pie was good."

  Scenario: A note with nothing typed is refused in plain words
    Given the drive has started at A
    When kris taps "Note" with nothing typed
    Then kris is told to say something first
    And no post was made

  Scenario: A note with words in it posts on the tap
    Given the drive has started at A
    When kris types the note "Filled up." and taps "Note"
    Then the post exists
    And the post is a "note"

  Scenario: A one-tap photo at a private place is fuzzed by default
    Given the drive has started at A
    And stop B is a private place
    When kris taps "Photo" and picks a photo taken at B's exact point
    Then the post is published as a fuzzed cell, not the exact point

  Scenario: The old extra tap is gone
    Given the drive has started at A
    When kris opens the post form
    Then the form has no post type radio row
    And the Post button is inside More options
    And everything else on the form is inside More options

  Scenario: Start mode still starts the drive
    Given a photo taken at 09:15 with its GPS at A
    When kris opens the post form
    Then the form is in start mode for A
    When kris starts the drive with that photo
    Then A is departed at 09:15 in A's zone
