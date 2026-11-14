Feature: The combination cap counts only the options in force
  A trip document may offer at most 64 combinations of choices; every one is
  parsed on a push. On 2026-11-13 adding one south choice to a four-day
  document was refused as "more than 64 combinations" because options tied to
  another option's choice (day 1, night 1 and day 2 only apply under the
  classic south) were multiplied in even where that choice is not taken. The
  cap now counts the plans that really differ. The cap itself stays 64.
  (Synthetic options: invented ids, no places, no addresses.)

  Scenario: A dead dependent option adds no plans of its own
    Given a synthetic document with a south option of 5 choices, three dependent options of 2 choices each that apply only under the classic south, and an independent option of 2 choices
    When the combinations are counted
    Then the document is accepted with 24 plans

  Scenario: A document with 65 independent combinations is still refused
    Given a synthetic document with independent options of 5 and 13 choices
    When the combinations are counted
    Then the document is refused as offering more than 64 combinations

  Scenario: A document with exactly 64 independent combinations is accepted
    Given a synthetic document with independent options of 8 and 8 choices
    When the combinations are counted
    Then the document is accepted with 64 plans
