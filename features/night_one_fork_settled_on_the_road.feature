Feature: Night 1 is a fork the trip-keeper settles on the road
  Kris, 2026-11-09: "We have options when driving, like Pittsburg (Greenburgh)
  vs. West Virginia. We can keep our options in our Plan and Ride dataset and
  make decisions enroute." Where the first night is spent changes the road of
  day 1 AND which day-2 roads make sense, so a trip document may tie a feature
  to several choices at once ("when": {"day1": "ridge", "night1": "far"}) and
  may make one option depend on another: an option with a "when" is in force
  only while the choice it names is selected, and off the plan page otherwise.
  Every place and coordinate here is invented.

  Background:
    Given a trip whose Night 1 is a fork between a near camp and a far hollow

  Scenario: Night 1 is a two-way choice and the day-2 roads are offered only while the night is near
    Then the plan page offers option "night1" with choices "camp" and "far"
    And the plan page offers option "day2"
    When the plan page is viewed with Night 1 as "far"
    Then the plan page does not offer option "day2"
    And the plan page still offers option "day1"

  Scenario: The far night makes day 1 a long day and shortens day 2, in the same number of days
    Then the plan under Night 1 "camp" has 3 days, and day 1 is not long
    And the plan under Night 1 "far" has 3 days, and day 1 is long
    And the plan under Night 1 "far" has 3 days, and day 2 is not long

  Scenario: Every reachable combination of choices is a plan that passes its checks
    Then the plan under "day1=valley night1=far" has 3 days, and day 1 is long
    And the plan under "day1=valley night1=camp day2=south" has 3 days, and day 1 is not long

  Scenario: After the drive starts and the shared stop is reached, switching Night 1 moves tonight's stop, the miles left and the Waze link, and keeps what was recorded
    Given the drive has started from Alder
    And the car has reached "Birch Gate"
    And a note was posted on the road
    And tonight's stop on the HUD is "Cedar Camp"
    And the HUD's Navigate in Waze goes to "Cedar Camp"
    When the trip-keeper saves Night 1 as "far"
    Then the save is accepted
    And tonight's stop on the HUD is "Far Hollow"
    And the HUD's Navigate in Waze goes to "Far Hollow"
    And the HUD's miles left grew
    And the saved plan's day 1 is 780 miles
    And "Alder" is still arrived at
    And "Birch Gate" is still arrived at
    And the note is still there
    And the saved selection has Night 1 "far"

  Scenario: Switching back to the near camp is also allowed while the night is still ahead
    Given the drive has started from Alder
    And the trip-keeper saves Night 1 as "far"
    And the save is accepted
    When the trip-keeper saves Night 1 as "camp"
    Then the save is accepted
    And tonight's stop on the HUD is "Cedar Camp"
    And the saved selection has Night 1 "camp"

  Scenario: A leg driven to its last stop keeps its road, posts and all
    Given the drive has started from Alder
    And the car has reached "Birch Gate"
    And the car has reached "Cedar Camp"
    And a note was posted on the road
    When the trip-keeper saves the day-1 road as "valley"
    Then the save is refused and says that leg 1 has been driven to its last stop
    And the note is still there

  Scenario: Once Night 1 is arrived at, it is no longer the plan's to move, and the refusal says why
    Given the drive has started from Alder
    And the car has reached "Birch Gate"
    And the car has reached "Cedar Camp"
    When the trip-keeper saves Night 1 as "far"
    Then the save is refused and says that checkpoint 3 is arrived
    And "Cedar Camp" is still arrived at
    And the saved selection has Night 1 "camp"

  Scenario: The same document pushed again after the switch is taken, and the switch stands
    Given the drive has started from Alder
    And the car has reached "Birch Gate"
    And the trip-keeper saves Night 1 as "far"
    And the save is accepted
    When the document is pushed again unchanged
    Then the push is taken
    And the saved selection has Night 1 "far"
    And tonight's stop on the HUD is "Far Hollow"

  Scenario: A document whose option depends on an option it never declares is refused whole
    When the document is pushed with the day-2 option depending on option "ghost"
    Then the push is refused as not a trip document, naming "ghost"

  Scenario: A feature that names a choice its option does not have is refused whole
    When the document is pushed with a leg tied to night1 "moon"
    Then the push is refused as not a trip document, naming "moon"
