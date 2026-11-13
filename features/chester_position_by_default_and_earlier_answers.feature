Feature: Chester sends the phone's position by default and keeps earlier answers on the page
  Kris, 2026-11-13, on the road: "When using Chester, the location locked
  should be the default; click the button to remove the GPS location from the
  query. Most (almost all) will want GPS locked. Put this info first, before
  the query." and "Provide access to previous query (ask) results."
  The position row is the first thing in the form. The page asks the phone at
  load and every ask carries a fresh fix; one button turns that off for the
  questions that follow, and a reload turns it back on, with nothing kept in
  the browser or on the server. The server's handling of lat and lng is
  unchanged: it coarsens near a private place and nothing renders a raw point.
  The page lists the signed-in driver's own recent asks on the active ride,
  each a details with its answer and cards; a proposal is shown as words, never
  with a Save. No scenario here reaches the real API.

  Scenario: The position row comes before the presets and the question
    Given Kris is signed in and Chester is on
    When Kris looks at the Chester page
    Then the position row comes before "Ask in one tap" and before "Or ask your own"

  Scenario: The page declares the position on by default and keeps no state of it in the browser
    Given Kris is signed in and Chester is on
    When Kris looks at the Chester page
    Then the page says "Your position goes with each question."
    And the position button offers "Ask without my position" and can be turned back to "Send my position"
    And the script keeps the position in no browser storage

  Scenario: An ask with a position still reaches the store as before
    Given Kris is signed in and Chester is on
    When Kris asks "Where is fuel?" with the position 41.0123 and -101.0456
    Then the row was logged with the source "browser"
    And no page shows the raw point 41.0123

  Scenario: An ask without a position is accepted
    Given Kris is signed in and Chester is on
    When Kris asks "Where is fuel?" without a position
    Then the ask was answered and logged with no phone position

  Scenario: An earlier ask is listed with its answer and its place card
    Given Kris is signed in and Chester is on
    And Kris asked "Where is fuel?" and was told "Birch Gate has fuel." with a card for "Birch Gate"
    When Kris looks at the Chester page
    Then the Earlier answers list shows "Where is fuel?" with "Birch Gate has fuel." and a card for "Birch Gate"

  Scenario: Another driver does not see the first driver's asks
    Given Kris is signed in and Chester is on
    And Kris asked "Where is fuel?" and was told "Birch Gate has fuel." with a card for "Birch Gate"
    When Dominic looks at the Chester page
    Then the Earlier answers list has nothing of Kris's

  Scenario: A signed-out visitor gets none of it
    Given Kris is signed in and Chester is on
    And Kris asked "Where is fuel?" and was told "Birch Gate has fuel." with a card for "Birch Gate"
    When a signed-out visitor asks for the Chester page
    Then the visitor is sent to sign in and sees no earlier answer

  Scenario: An earlier answer with a proposal shows its words and no Save
    Given Kris is signed in and Chester is on
    And Kris asked "Change the route" and Chester proposed "Take the I-81 way"
    When Kris looks at the Chester page
    Then the Earlier answers list shows the proposal words "Take the I-81 way" and no Save button
