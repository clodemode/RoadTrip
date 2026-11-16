Feature: Time flows oldest to newest, and the latest is on the first screen
  Kris, 2026-11-16: most lists of photos and songs were upside down, newest at
  the top. Time flows the other way, so every timeline and photo list now reads
  oldest first, top to bottom, and Previous and Next in the photo viewer mean
  older and newer. A visitor who lands in the middle of a trip does not have to
  scroll past days of history to find where it is now: the first screen of the
  Ride page carries a strip of three small tiles, the latest point, the latest
  song and the latest photo, and each tile links to its item. The signed-in
  driver sees the same strip. A tile with nothing to show is not drawn, and a
  trip with none of the three has no strip at all. The strip names a place and
  a time in that place's own zone and never a coordinate.

  Background:
    Given a trip under way that left Alder at 13:00 UTC on its first day
    And a photo post "Morning coffee" at Birch Gate at 14:00 UTC
    And a note post "Noon note" with no pin at 17:00 UTC
    And a photo post "Evening view" at Dogwood at 23:30 UTC
    And the song "First Song" by "The Larks" played at 15:00 UTC
    And the song "Second Song" by "The Wrens" played at 23:40 UTC

  Scenario: A visitor's first screen carries the latest point, song and photo
    When a visitor opens the Ride page
    Then the latest strip is drawn before the map
    And the point tile names "Dogwood" at "Wed May 1 · 5:30 PM MDT"
    And the song tile reads "Last played" and names "Second Song" by "The Wrens"
    And the song tile links to the Listen Inn
    And the photo tile shows the photo of "Evening view" and links to its viewer
    And the latest strip carries no coordinates

  Scenario: Each tile links down to its item on the page
    When a visitor opens the Ride page
    Then the point tile links to the post "Evening view" on the timeline
    And that post is on the page under the same anchor

  Scenario: An arrival newer than every post is the latest point
    Given the car arrived at Elm at 02:00 UTC the next day
    When a visitor opens the Ride page
    Then the point tile names "Elm" at "Wed May 1 · 8:00 PM MDT"
    And the point tile links to the stop "Elm" in the checkpoint list
    And that stop is on the page under the same anchor

  Scenario: A post that hides its location is never the latest point
    Given a note post "Somewhere quiet" that hides its location at 23:50 UTC
    When a visitor opens the Ride page
    Then the point tile names "Dogwood" at "Wed May 1 · 5:30 PM MDT"

  Scenario: The signed-in driver sees the same strip
    When the trip-keeper opens the Ride page
    Then the latest strip is drawn before the map
    And the point tile names "Dogwood" at "Wed May 1 · 5:30 PM MDT"
    And the photo tile shows the photo of "Evening view" and links to its viewer

  Scenario: The Ride page timeline reads oldest first
    When a visitor opens the Ride page
    Then the timeline lists "Morning coffee", "Noon note", "Evening view" in that order

  Scenario: Two posts at the same minute keep the order they were made in
    Given a note post "Twin one" with no pin at 18:00 UTC
    And a note post "Twin two" with no pin at 18:00 UTC
    When a visitor opens the Ride page
    Then the timeline lists "Noon note", "Twin one", "Twin two", "Evening view" in that order

  Scenario: A leg page timeline reads oldest first
    When a visitor opens the first leg's timeline
    Then the timeline lists "Morning coffee", "Noon note" in that order

  Scenario: The photos page reads oldest first
    Given a photo post "Lunch stop" at Cedar Camp at 18:30 UTC
    When a visitor opens the photo gallery
    Then the gallery lists "Morning coffee", "Lunch stop", "Evening view" in that order

  Scenario: The driver's Remove photos page reads oldest first
    Given a photo post "Lunch stop" at Cedar Camp at 18:30 UTC
    When the trip-keeper opens the Remove photos page
    Then the gallery lists "Morning coffee", "Lunch stop", "Evening view" in that order

  Scenario: The post census reads oldest first
    When the trip-keeper reads the post census
    Then the census lists "Morning coffee", "Noon note", "Evening view" in that order

  Scenario: Previous is the older photo and Next is the newer one
    Given a photo post "Lunch stop" at Cedar Camp at 18:30 UTC
    When a visitor opens the viewer of "Lunch stop"
    Then Previous leads to the photo of "Morning coffee"
    And Next leads to the photo of "Evening view"

  Scenario: The oldest photo has no Previous and the newest has no Next
    When a visitor opens the viewer of "Morning coffee"
    Then the viewer offers no Previous
    And Next leads to the photo of "Evening view"
    When a visitor opens the viewer of "Evening view"
    Then the viewer offers no Next
    And Previous leads to the photo of "Morning coffee"

  Scenario: Saved music reads oldest first
    Given the trip-keeper favourited "Old favourite" and then "New favourite"
    When the trip-keeper opens the plan page
    Then the saved music lists "Old favourite", "New favourite" in that order

  Scenario: A finished trip keeps the strip as a record
    Given the trip is finished at Elm at 02:00 UTC the next day
    When a visitor opens the Ride page
    Then the point tile names "Elm" at "Wed May 1 · 8:00 PM MDT"
    And the song tile reads "Last played" and names "Second Song" by "The Wrens"
    And the photo tile shows the photo of "Evening view" and links to its viewer
    And the page draws no now-playing chip

  Rule: A tile with nothing to show is not drawn

    Scenario: A trip with no posts, no arrivals, no plays and no photos has no strip
      Given a trip that has not started and holds nothing
      When a visitor opens the Ride page
      Then the page draws no latest strip

    Scenario: A trip with only a song shows only the song tile
      Given a trip that has not started and holds nothing
      And the song "Lonely Song" by "The Owls" played at 15:00 UTC
      When a visitor opens the Ride page
      Then the song tile reads "Last played" and names "Lonely Song" by "The Owls"
      And the page draws no point tile and no photo tile

    Scenario: A trip with posts but no photo shows no photo tile
      Given a trip that has not started and holds nothing
      And a note post "Only words" at Birch Gate at 14:00 UTC
      When a visitor opens the Ride page
      Then the point tile names "Birch Gate" at "Wed May 1 · 9:00 AM CDT"
      And the page draws no photo tile

    Scenario: A song that started just now reads as playing now
      Given a trip that has not started and holds nothing
      And the song "Fresh Song" by "The Finches" started a minute ago
      When a visitor opens the Ride page
      Then the song tile reads "Now playing" and names "Fresh Song" by "The Finches"
