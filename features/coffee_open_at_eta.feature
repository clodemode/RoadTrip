Feature: Coffee and food are independent and open when the car gets there
  Kris, 2026-11-14: the coffee finder should find independent shops that will
  be open when we reach them. A candidate whose opening hours are known and
  closed at the time the car reaches it is never sent to Chester; one whose
  hours are not known is kept and marked "hours unknown". Whether a place is a
  chain is Chester's judgement, told in the taste line. TomTom and the
  Messages API are local stubs.

  Background:
    Given a driver heading to "hudson bend" at "2026-11-07T15:15:00-06:00"
    And Chester is on and will keep no picks

  Scenario: A coffee shop closed when we get there is never sent to Chester
    Given TomTom finds the coffee shop "Closed Bean" open until "14:00" today
    And TomTom finds the coffee shop "Open Bean" open until "18:00" today
    And TomTom finds the coffee shop "Unlisted Bean" with no opening hours
    When the driver taps Find picks
    Then the picks request names "Open Bean"
    And the picks request does not name "Closed Bean"

  Scenario: A coffee shop with no hours is sent marked hours unknown
    Given TomTom finds the coffee shop "Open Bean" open until "18:00" today
    And TomTom finds the coffee shop "Unlisted Bean" with no opening hours
    When the driver taps Find picks
    Then the picks request names "Unlisted Bean" with "hours unknown"
    And the picks request names "Open Bean" without "hours unknown"

  Scenario: The search asks TomTom for hours and Chester is told to want independents
    Given TomTom finds the coffee shop "Open Bean" open until "18:00" today
    When the driver taps Find picks
    Then TomTom was asked for opening hours
    And the picks request says "independent local coffee shops only, never chains"
    And TomTom was asked for "cafe"
