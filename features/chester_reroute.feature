Feature: Chester proposes a switch among the trip's planned route choices
  Chester is told the trip's route choices and which one is selected. When the
  question bears on the route it may propose one switch as a card. The
  trip-keeper decides: Save posts the full selection through the plan save,
  and Chester changes nothing. Miles and hours are the plan's estimate, not
  traffic. A road that is already driven is locked, and a road being driven
  now is called out.

  Scenario: Chester is told the route choices, the selected one and what each would change
    Given the trip has route choices
    And Chester will say "Stay on the ridge."
    When the trip-keeper asks Chester about the road "Which road should I take on day one?"
    Then Chester was told the route choices
    And Chester was told "valley" would change the whole trip by "-60.0 miles"
    And Chester was told "ridge" is "selected"

  Scenario: A proposed switch renders as a card with its miles and hours
    Given the trip has route choices
    And Chester will propose switching "day1" to "valley" because "It is the shorter road."
    And Chester will then say "The valley road is shorter; tap Save to switch."
    When the trip-keeper asks Chester about the road "Which road should I take on day one?"
    Then the answer carries a proposal for "day1" to "valley"
    And the proposal changes the trip by "-60.0" miles and "-1.0" hours
    And the proposal says "It is the shorter road."
    And the answer was written after the proposal
    And the page builds the proposal card with a Save button that posts to the plan save

  Scenario: An invalid proposal is dropped
    Given the trip has route choices
    And Chester will propose switching "day1" to "nowhere" because "A road that is not there."
    And Chester will then say "I could not propose that."
    When the trip-keeper asks Chester about the road "Which road should I take on day one?"
    Then the answer carries no proposal
    And Chester was told "no such choice"
    And the route answer says "I could not propose that."
    And the log row's note says "a proposal was dropped"

  Scenario: A trip with no choices offers no propose tool and no route choices
    Given the trip has no route choices
    And Chester will say "Straight on."
    When the trip-keeper asks Chester about the road "Which road should I take?"
    Then Chester was offered no propose tool
    And Chester was told no route choices

  Scenario: Chester is told a choice is locked and a proposal for it is dropped
    Given the trip has route choices
    And a post is already made on leg 1
    And day 1 has been driven to its last stop
    And Chester will propose switching "day1" to "valley" because "It is shorter."
    And Chester will then say "That road is already driven."
    When the trip-keeper asks Chester about the road "Can I switch day one?"
    Then Chester was told "valley" is "LOCKED"
    And the answer carries no proposal
    And Chester was told "Route me here"

  Scenario: A leg being driven reads IN PROGRESS and its card says so
    Given the trip has route choices
    And the drive has started on day 1
    And Chester will propose switching "day1" to "valley" because "The traveller asked outright."
    And Chester will then say "Tap Save to switch."
    When the trip-keeper asks Chester about the road "Switch to the valley road now"
    Then Chester was told "day1" is "IN PROGRESS"
    And the answer carries a proposal for "day1" to "valley"
    And the proposal card is marked in progress

  Scenario: The proposal is logged
    Given the trip has route choices
    And Chester will propose switching "day1" to "valley" because "It is the shorter road."
    And Chester will then say "Tap Save to switch."
    When the trip-keeper asks Chester about the road "Which road should I take on day one?"
    Then the log row records the proposal "day1" to "valley" as "proposed"

  Scenario: Tapping Save applies through the plan save
    Given the trip has route choices
    And Chester will propose switching "day1" to "valley" because "It is the shorter road."
    And Chester will then say "Tap Save to switch."
    When the trip-keeper asks Chester about the road "Which road should I take on day one?"
    And the trip-keeper taps Save on the proposal
    Then the plan save answers "ok"
    And the saved plan takes "valley" for "day1"

  Scenario: A refused save says why and changes nothing
    Given the trip has route choices
    And Chester will propose switching "day1" to "valley" because "It is the shorter road."
    And Chester will then say "Tap Save to switch."
    When the trip-keeper asks Chester about the road "Which road should I take on day one?"
    And a post is made on leg 1 before the trip-keeper taps Save
    And day 1 has been driven to its last stop
    And the trip-keeper taps Save on the proposal
    Then the plan save answers "refused" with a reason
    And the saved plan takes "ridge" for "day1"

  Scenario: A saved proposal is recorded as saved on its log row
    Given the trip has route choices
    And Chester will propose switching "day1" to "valley" because "It is the shorter road."
    And Chester will then say "Tap Save to switch."
    When the trip-keeper asks Chester about the road "Which road should I take on day one?"
    And the trip-keeper taps Save on the proposal
    Then the plan save answers "ok"
    And the log row's proposal outcome is "saved"
    And the page's Save sends the card's query id

  Scenario: A refused save is recorded as refused with why, and the plan is unchanged
    Given the trip has route choices
    And Chester will propose switching "day1" to "valley" because "It is the shorter road."
    And Chester will then say "Tap Save to switch."
    When the trip-keeper asks Chester about the road "Which road should I take on day one?"
    And a post is made on leg 1 before the trip-keeper taps Save
    And day 1 has been driven to its last stop
    And the trip-keeper taps Save on the proposal
    Then the plan save answers "refused" with a reason
    And the log row's proposal outcome is "refused" with a reason
    And the saved plan takes "ridge" for "day1"

  Scenario: A save with an unknown Chester id still saves and answers as before
    Given the trip has route choices
    And Chester will propose switching "day1" to "valley" because "It is the shorter road."
    And Chester will then say "Tap Save to switch."
    When the trip-keeper asks Chester about the road "Which road should I take on day one?"
    And the trip-keeper taps Save on the proposal with the Chester id "999999"
    Then the plan save answers "ok"
    And the plan save response carries only status, selection and plan
    And the saved plan takes "valley" for "day1"
    And the log row's proposal outcome is "proposed"
