Feature: A post taken on the evening of arrival stays in the arriving day's leg
  Production review 2026-11-13: Thursday night in Nashville, posts 63 to 65
  sat in the Winchester to Nashville leg but posts 66 to 68, taken at 10:58
  to 11:00 PM CST at the same place, sat in the next leg and showed under
  Friday. The leg a new post lands on was judged only in the zone each leg
  starts in; Eastern midnight passed an hour before Central midnight, no leg
  matched, and the fallback was the last leg. The day is now also judged in
  the zone the car is in, so the evening belongs to the day the car arrived.
  (Synthetic trip: invented zones and dates.)

  Scenario: The 11 PM post at the night stop stays in the arriving leg
    Given a two-day synthetic trip whose first leg starts Eastern and whose night stop is Central
    And the car has arrived at the night stop
    When a post is started at 11:02 PM Central on the evening of arrival
    Then the post lands in the arriving day's leg

  Scenario: An early evening post at the night stop stays in the arriving leg
    Given a two-day synthetic trip whose first leg starts Eastern and whose night stop is Central
    And the car has arrived at the night stop
    When a post is started at 9:30 PM Central on the evening of arrival
    Then the post lands in the arriving day's leg

  Scenario: The next morning's post lands in the next day's leg
    Given a two-day synthetic trip whose first leg starts Eastern and whose night stop is Central
    And the car has arrived at the night stop
    When a post is started at 6:30 AM Central the morning after
    Then the post lands in the next day's leg
