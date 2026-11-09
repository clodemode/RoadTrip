Feature: The menu follows the ride's state, Drive Tracks rides along, every page says where you are
  Kris, 2026-11-09: tighten the menu bar. A ride is Active (a simulated
  drive counts) or Planning. Drive Tracks is on every page while the ride is
  Active (in the player shell, see continuous_music_and_one_card.feature). Each page shows a breadcrumb and a page title below the banner.
  The state is read from the store, never from dates: Active is a started
  drive whose finish is not yet reached or skipped.

  Background:
    Given a trip with a start, two stops and a finish, a music library, and Chester switched on
    And the menu trip-keeper Kris signs in

  Scenario: Planning before the drive starts
    Then the ride state reads "Planning"

  Scenario: Active after a real Start
    When the drive is started
    Then the ride state reads "Active"

  Scenario: Active with simulation after a simulated Start
    Given the menu site is a KATE
    When the drive is started by the simulated car
    Then the ride state reads "Active, simulation"

  Scenario: Planning again once the finish is reached
    When the drive is started
    And the finish is reached
    Then the ride state reads "Planning"

  Scenario: Planning again once the finish is skipped
    When the drive is started
    And the finish is skipped
    Then the ride state reads "Planning"

  Scenario: The Active menu
    When the drive is started
    Then Kris's menu reads "Ride, Driver, Post, Chester, Lodging, Photos, Plan, GeoJSON"

  Scenario: The Planning menu keeps Post to start the drive and drops Chester
    Then Kris's menu reads "Ride, Driver, Post, Plan, Lodging, Photos, GeoJSON"

  Scenario: The Planning menu after the finish drops Post and Chester
    When the drive is started
    And the finish is reached
    Then Kris's menu reads "Ride, Driver, Plan, Lodging, Photos, GeoJSON"

  Scenario: Only an admin's menu carries Users
    When the drive is started
    And an admin opens "/"
    Then the menu reads "Ride, Driver, Post, Chester, Lodging, Photos, Plan, GeoJSON, Users"

  Scenario: The current page is marked
    When Kris opens "/plan"
    Then exactly one menu link is marked as the current page, "Plan"

  Scenario: A visitor never gets Post, Chester, Lodging or the strip
    When the drive is started
    And a visitor opens "/"
    Then the menu reads "Ride, Photos, Plan"
    And the page has no Drive Tracks strip
    And the page has no link to "/driver/post"
    And the page has no link to "/chester"

  Scenario: A visitor sees the state, but never the word simulation
    Given the menu site is a KATE
    When the drive is started by the simulated car
    And a visitor opens "/"
    Then the page's ride state reads "Active"

  # Since 2026-11-09 a signed-in page while Active is the player shell (see
  # continuous_music_and_one_card.feature); these scenarios read the page the
  # shell's frame shows, which carries no card and no audio element.

  Scenario Outline: No Drive Tracks card while Planning
    When Kris opens "<path>"
    Then the page has no Drive Tracks strip

    Examples:
      | path         |
      | /            |
      | /plan        |
      | /photos      |
      | /driver/post |

  Scenario: The driver screen inside the frame carries one search card, no card of its own and no duplicate ids
    When the drive is started
    And Kris opens "/driver"
    Then the page has no Drive Tracks strip
    And the page has exactly one RoadTracks search card
    And no element id appears twice on the page

  Scenario Outline: Every page has one breadcrumb and one title below the banner
    When Kris opens "<path>"
    Then the page has exactly one breadcrumb and exactly one h1
    And the breadcrumb reads "<crumb>"
    And the h1 reads "<title>"

    Examples:
      | path             | crumb                              | title                   |
      | /                | Alder to Elm > Ride                   | Ride                    |
      | /plan            | Alder to Elm > Plan                   | Plan                    |
      | /photos          | Alder to Elm > Photos                 | Photos                  |
      | /checklist       | Alder to Elm > Checklist              | Checklist               |
      | /chester         | Alder to Elm > Chester                | Chester                 |
      | /driver          | Alder to Elm > Driver                 | Driver                  |
      | /driver/post     | Alder to Elm > Driver > Start the drive | Start the drive       |
      | /driver/lodging  | Alder to Elm > Lodging                | Lodging                 |
      | /legs/day-1-ridge | Alder to Elm > Legs > Alder → Cedar Camp    | Alder → Cedar Camp             |

  Scenario: Sign-in carries its breadcrumb too
    When a visitor opens "/driver/login"
    Then the page has exactly one breadcrumb and exactly one h1
    And the breadcrumb reads "Alder to Elm > Sign in"

  Scenario: The admin's Users page carries its breadcrumb too
    When an admin opens "/driver/users"
    Then the page has exactly one breadcrumb and exactly one h1
    And the breadcrumb reads "Alder to Elm > Users"
    And the h1 reads "Users"
