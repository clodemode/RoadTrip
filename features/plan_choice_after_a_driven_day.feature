Feature: A plan choice that leaves every driven day exactly as it is can be saved on the road
  Kris, 2026-11-12, day 2 of the drive: day 1 is the same road under every way
  south, and it has been driven, but where to sleep on night 2 is the very
  decision the options were written for. The trip document writes day 1 once
  for each choice, under its own leg name, so the plan guard read the other
  choice's day 1 as a different road and refused the save. A leg is the same
  leg when its name, date, miles, hours, road line and stops (order, name,
  point, zone and precision) are all the same, whatever the document called
  it. Then the choice is taken and the days before it are not touched. Any
  difference at all in a driven day is still refused whole. Every place here is
  invented.

  Background:
    Given a trip whose second night is a choice after an identical first day

  Scenario: With day 1 driven and holding posts, the other second night is saved and day 2 ends there
    Given day 1 has been driven to its last stop and holds 2 pinned posts
    When the trip-keeper saves the second night as "far"
    Then the second-night save is taken
    And day 2 ends at "Fir Landing" and day 3 starts there
    And the saved selection has the second night "far"

  Scenario: Day 1's posts, pins, arrivals, routing points and private note come through the save untouched
    Given day 1 has been driven to its last stop and holds 2 pinned posts
    When the trip-keeper saves the second night as "far"
    Then the second-night save is taken
    And day 1's posts are the same rows with the same pins and words
    And day 1's stops are the same rows, each arrived at the same moment
    And day 1's leg is the same row with its routing points on the same rows
    And the private note on a day 1 stop is as it was

  Scenario: Saving back to the first choice is also taken
    Given day 1 has been driven to its last stop and holds 2 pinned posts
    And the trip-keeper saves the second night as "far"
    And the second-night save is taken
    When the trip-keeper saves the second night as "near"
    Then the second-night save is taken
    And day 2 ends at "Dogwood" and day 3 starts there
    And day 1's posts are the same rows with the same pins and words

  Scenario: A choice that is saved again is taken and still changes nothing driven
    Given day 1 has been driven to its last stop and holds 2 pinned posts
    When the trip-keeper saves the second night as "near"
    Then the second-night save is taken
    And day 1's posts are the same rows with the same pins and words

  Scenario Outline: A choice whose first day differs is still refused whole, with today's sentence
    Given day 1 has been driven to its last stop and holds 2 pinned posts
    When the trip-keeper saves the second night as "<choice>"
    Then the second-night save is refused saying "leg 1 holds 2 post(s) and has been driven to its last stop, so its road does not change"
    And day 2 still ends at "Dogwood"
    And the saved selection has the second night "near"
    And day 1's posts are the same rows with the same pins and words

    Examples:
      | choice | what differs                 |
      | bent   | the road line of day 1       |
      | moved  | the point of day 1's last stop |

  Scenario: A choice that changes a day with one arrived stop in its middle is still refused
    Given only the middle stop of day 1 has been reached and holds a post
    When the trip-keeper saves the second night as "detour"
    Then the second-night save is refused saying "checkpoint 2 is arrived"
    And day 2 still ends at "Dogwood"
    And day 1's posts are the same rows with the same pins and words

  Scenario: Chester's route choices no longer call the choice with the same first day locked
    Given day 1 has been driven to its last stop and holds 2 pinned posts
    When Chester is asked about the second night with the model stubbed
    Then Chester's route choices call "far" open and "bent" and "moved" LOCKED
