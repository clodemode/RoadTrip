Feature: A driver's ride assignment holds on every write
  Audit, 2026-11-16: a driver assigned to one ride could write to another. The
  ride check trusted a ride named in the query string that no handler read, the
  routes that take a post by its number never asked which ride the post is on,
  and the trip-keeper's own routes (push a trip, switch the ride the site
  shows, replace the private overlay, list every ride) asked only for a
  sign-in. The ride a request acts on is the ride the site shows; nothing in
  the address can name another.

  Background:
    Given a site showing the fixture ride, with a second ride in its database
    And driver ann is assigned to the ride the site shows
    And driver bob is assigned only to the second ride

  Scenario Outline: A driver of another ride cannot write to the ride the site shows by any address
    When bob sends a new post to "<address>"
    Then the site answers 403
    And the ride the site shows has no new post

    Examples:
      | address                         |
      | /driver/post                    |
      | /driver/post?ride=SECOND        |
      | /driver/post?ride=SECOND&ride=x |

  Scenario Outline: A post of another ride is refused by its number
    Given a post on the second ride
    When ann sends "<method>" to that post's "<action>"
    Then the site answers 404
    And that post is unchanged

    Examples:
      | method | action       |
      | POST   | delete       |
      | POST   | edit         |
      | GET    | edit         |
      | POST   | pin          |
      | POST   | fuzz         |
      | POST   | unfuzz       |
      | POST   | photo/remove |
      | POST   | photo/undo   |

  Scenario: A driver still works on a post of their own ride
    Given a post on the ride the site shows
    When ann sends "POST" to that post's "delete"
    Then the site answers 303
    And that post is gone

  Scenario Outline: The trip-keeper's routes refuse a driver and still take the push token and an admin
    When ann sends "<method>" to "<address>"
    Then the site answers 403
    And the push token sending "<method>" to "<address>" is not refused
    And an admin sending "<method>" to "<address>" is not refused

    Examples:
      | method | address             |
      | POST   | /driver/trip        |
      | POST   | /driver/ride/active |
      | POST   | /driver/private     |
      | GET    | /driver/rides.json  |
