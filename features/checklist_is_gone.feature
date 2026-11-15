Feature: The checklist is gone
  Kris, 2026-11-14: the checklist was never really used, and trip paperwork is
  managed in CoWork. The feature is purged: no page, no JSON, no link.

  Background:
    Given a ride with a start, two stops and a finish, Chester on, music played and a post made
    And Kris is signed in as the trip-keeper

  Scenario Outline: A checklist address answers not found
    When Kris asks for "<path>"
    Then the answer is 404

    Examples:
      | path                  |
      | /checklist            |
      | /checklist.json       |
      | /driver/checklist     |
      | /driver/checklist.json |

  Scenario: No page links to a checklist
    When Kris reads the home page, the driver screen, the plan and the Chester page
    Then none of them links to a checklist
