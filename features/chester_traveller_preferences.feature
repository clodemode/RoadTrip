Feature: Chester knows the traveller's standing preferences and helps on the way and at the day's end
  Kris, 2026-11-11, driving Kingston to Texas: "We are hoping Chester also
  finds nice enroute things to do without adding much miles and helps us find
  towns with lower costs for decent lodging as we get close to days end."
  The standing preferences are one server-side record for the ride that is
  active: the day's-end window, the lodging price band, places to avoid, who
  navigates, and the most extra minutes a thing on the way may add. The
  signed-in trip-keeper sets them on the Chester page; a machine sets them
  with the same POST under the same auth. They go to the API in the one place
  the context is built, as a short PREFERENCES block. Chester only suggests:
  the plan changes only by the trip-keeper's tap.
  A Stop is where we sleep, a Milestone is a place passed, a Waypoint aids
  routing (naming-roadtrip-a-stop-is-where-we-sleep-a-milestone-marks-progress-and-a-waypoint-aids-routing).
  No scenario here reaches the real API: the Messages API is the stub.

  Scenario: Saved preferences are told to the API and the prompt version changed
    Given the trip-keeper has saved Kris's preferences
    And the stubbed API will answer "Birch Gate has fuel."
    When the trip-keeper puts "Where is fuel?" to Chester
    Then the API was told a PREFERENCES block
    And the PREFERENCES block says "Stop driving for the day between 19:00 and 21:00"
    And the PREFERENCES block says "80 to 120 dollars a night"
    And the PREFERENCES block says "Places to avoid: the south shores of Lake Ontario and Lake Erie"
    And the PREFERENCES block says "Navigation: Waze"
    And the PREFERENCES block says "at most 20 extra minutes"
    And the prompt version on the row is "chester-rt-14"

  Scenario: With no preferences saved the API is told nothing new
    Given the stubbed API will answer "Birch Gate has fuel."
    When the trip-keeper puts "Where is fuel?" to Chester
    Then the API was told no PREFERENCES block

  Scenario: Saving an empty set takes the preferences away
    Given the trip-keeper has saved Kris's preferences
    And the trip-keeper saves every preference blank
    And the stubbed API will answer "Birch Gate has fuel."
    When the trip-keeper puts "Where is fuel?" to Chester
    Then the API was told no PREFERENCES block

  Scenario: The free-text line is stripped of addresses and number strings
    Given the trip-keeper saves the places to avoid as "no toll roads, and not 12 Elm Street, call 555-1212 or me@example.com"
    And the stubbed API will answer "Birch Gate has fuel."
    When the trip-keeper puts "Where is fuel?" to Chester
    Then the PREFERENCES block says "Places to avoid: no toll roads"
    And the API was told nothing of "Elm" or "555" or "example.com" or "12"

  Scenario Outline: A bad value is refused and the field is named
    When the trip-keeper saves "<field>" as "<value>"
    Then the save is refused naming "<field>"
    And nothing was saved

    Examples:
      | field         | value                  |
      | day_end_from  | 7pm                    |
      | day_end_to    | 25:00                  |
      | price_low     | cheap                  |
      | price_high    | 99999                  |
      | max_extra_min | -5                     |
      | max_extra_min | 400                    |
      | avoid         | AVOIDLONG              |
      | navigator     | NAVLONG                |

  Scenario: A window that ends before it starts, or half a window, is refused naming the field
    When the trip-keeper saves "day_end_from" as "21:00" with "day_end_to" as "19:00"
    Then the save is refused naming "day_end_to"
    When the trip-keeper saves "day_end_from" as "19:00" with "day_end_to" as ""
    Then the save is refused naming "day_end_to"
    When the trip-keeper saves "price_low" as "120" with "price_high" as "80"
    Then the save is refused naming "price_high"

  Scenario: The two buttons send their fixed sentences through the ordinary ask
    Given the stubbed API will answer "Here is what I found."
    When the trip-keeper taps "Things to do on the way today" on the Chester page
    Then the API was asked the sentence on that button
    And the sentence on that button says "extra minutes"
    And the answer was logged as an ordinary preset question
    Given the stubbed API will answer "Here is where to sleep."
    When the trip-keeper taps "Where to sleep tonight" on the Chester page
    Then the API was asked the sentence on that button
    And the sentence on that button says "price band"
    And the answer was logged as an ordinary preset question

  Scenario: The Chester page shows the saved preferences in a plain form
    Given the trip-keeper has saved Kris's preferences
    When the trip-keeper opens the Chester page
    Then the form is labelled in plain words
    And the form shows the saved values
    And the page keeps no preference in the browser and opens no dialog

  Scenario: A signed-out visitor can neither see nor save preferences
    Given the trip-keeper has saved Kris's preferences
    When a signed-out visitor asks for the preferences and tries to save some
    Then the visitor is sent to sign in or refused, and sees no preference
    And the saved preferences are unchanged

  Scenario: A machine sets them with the PUSH_TOKEN bearer and reads them back
    When a machine posts Kris's preferences with the bearer token
    Then the machine is told they are saved, with the values
    And the server holds one record for the active ride
    And reading them back with the bearer token gives the same values

  Scenario: Chester is told how to word the day and how to find things on the way and for tonight
    Given the stubbed API will answer "Birch Gate has fuel."
    When the trip-keeper puts "Where is fuel?" to Chester
    Then the system prompt defines Stop, Milestone and Waypoint
    And the system prompt asks for extra minutes on things to do and leaves out what is past the limit
    And the system prompt asks for towns before and after the Stop with lodging inside the price band, saying where each price was seen
    And the system prompt never states a price as certain
    And the system prompt says Chester only suggests and the plan changes by the trip-keeper's tap
