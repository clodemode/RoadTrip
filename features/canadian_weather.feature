Feature: The Weather chip answers honestly on a Canadian leg
  The US National Weather Service refuses a point outside the United States
  with a 400 that says "out of bounds". On the Kingston to Toronto leg that
  made the driver screen read "Weather: unknown (error)". Where, and only
  where, the US source says a point is out of its coverage, the server asks
  Environment Canada for that point. A failure of the source that covers a
  point still reads unknown, never clear.

  Scenario: A Canadian point with an active warning shows it as a chip
    Given a sample point at 44.2300,-76.4800
    And the US source calls that point out of bounds
    And Environment Canada has an active warning "wind warning" for "Testville" at that point
    When the weather chips are asked for
    Then the chips read:
      """
      items|Weather: Wind warning, Testville
      """
    And Environment Canada was asked once, with the project's public identity and a small box around the point

  Scenario: A Canadian point with nothing active reads clear
    Given a sample point at 44.2300,-76.4800
    And the US source calls that point out of bounds
    When the weather chips are asked for
    Then the chips read:
      """
      clear|Weather: no active alerts
      """

  Scenario: Environment Canada failing on a Canadian point reads unknown, never clear
    Given a sample point at 44.2300,-76.4800
    And the US source calls that point out of bounds
    And Environment Canada fails at that point
    When the weather chips are asked for
    Then the chips read:
      """
      unknown|Weather: unknown (error)
      """

  Scenario: A US server error is not mistaken for being outside US coverage
    Given a sample point at 30.2000,-97.7000
    And the US source answers 500 at that point
    When the weather chips are asked for
    Then the chips read:
      """
      unknown|Weather: unknown (error)
      """
    And Environment Canada was not asked

  Scenario: A US bad request that is not about coverage is not sent to Canada either
    Given a sample point at 30.2000,-97.7000
    And the US source answers 400 about something else at that point
    When the weather chips are asked for
    Then the chips read:
      """
      unknown|Weather: unknown (error)
      """
    And Environment Canada was not asked

  Scenario: A leg with one US and one Canadian point shows both alerts
    Given a sample point at 30.2000,-97.7000
    And the US source has an active "Flood Warning" for "Travis County" at that point
    And a sample point at 44.2300,-76.4800
    And the US source calls that point out of bounds
    And Environment Canada has an active advisory "frost advisory" for "Testville" at that point
    When the weather chips are asked for
    Then the chips read:
      """
      items|Weather: Flood Warning, Travis County
      items|Weather: Frost advisory, Testville
      """
    And Environment Canada was asked once, with the project's public identity and a small box around the point

  Scenario: A warning outranks a watch outranks an advisory, and a statement ranks with an advisory
    Given a sample point at 44.2300,-76.4800
    And the US source calls that point out of bounds
    And Environment Canada has an active advisory "frost advisory" for "Testville" at that point
    And Environment Canada has an active statement "special weather statement" for "Testville" at that point
    And Environment Canada has an active watch "snow squall watch" for "Testville" at that point
    And Environment Canada has an active warning "wind warning" for "Testville" at that point
    When the weather chips are asked for
    Then the chips read:
      """
      items|Weather: Wind warning, Testville
      items|Weather: Snow squall watch, Testville
      items|Weather: Frost advisory, Testville
      items|Weather: Special weather statement, Testville
      """

  Scenario: A cancelled or an expired Canadian alert shows no chip
    Given a sample point at 44.2300,-76.4800
    And the US source calls that point out of bounds
    And Environment Canada has a cancelled warning "wind warning" for "Testville" at that point
    And Environment Canada has an expired warning "rainfall warning" for "Testville" at that point
    When the weather chips are asked for
    Then the chips read:
      """
      clear|Weather: no active alerts
      """
