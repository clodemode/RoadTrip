Feature: A tapped photo opens fitted to the screen
  A photo on the timeline used to link to the full-size file, which a phone
  shows at actual size, a corner of the picture. It now opens a small page that
  shows the whole picture fitted to the screen, with a way back and a link to
  the original. The page answers only what the timeline already shows.

  Background:
    Given kris is signed in
    And the active trip has milestones A, B and C in order, with A already arrived at
    And kris has posted a photo

  Scenario: A visitor's timeline photo opens the viewer, not the raw file
    When a visitor opens the first leg's page
    Then the photo links to its viewer page
    And no link on the page goes straight to a full-size photo file

  Scenario: The viewer shows the whole photo with a way back and the original
    When a visitor opens the photo's viewer page
    Then the viewer holds the photo with the fit-to-screen class
    And the viewer has a link back to the post on its leg page
    And the viewer has a link called "Open the original"
    And the viewer carries no coordinates

  Scenario: A photo of a post the timeline does not show is not served
    Given the same photo is on a post of a ride the site is not showing
    When a visitor opens the viewer page of that post
    Then the answer is "not found"

  Scenario: A post that does not exist is not found
    When a visitor opens the viewer page of post 987654
    Then the answer is "not found"

  Scenario: The photos page and the map send a tap to the viewer too
    When a visitor opens the photos page
    Then the photo links to its viewer page
    And the map's photo popup links to the viewer, not the original

  Scenario: The signed-in driver gets the same viewer
    When kris opens the photo's viewer page
    Then the viewer holds the photo with the fit-to-screen class
    And the viewer has a link called "Open the original"
