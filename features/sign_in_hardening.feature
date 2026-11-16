Feature: Sign-in does not send people elsewhere, cannot be guessed at speed, and can be ended
  Audit, 2026-11-16: after a sign-in the site sent the browser to whatever
  "next" said so long as it began with one slash, and browsers read a
  backslash or a tab after that slash as the start of another site. Nothing
  slowed a run of wrong passwords, the form took a body of any size, and a
  session, once issued, could not be ended for thirty days.

  Background:
    Given a site with user ann whose password is known

  Scenario Outline: A next address that leaves the site is refused
    When ann signs in with next "<next>"
    Then she lands on "<lands>"

    Examples:
      | next                  | lands   |
      | /plan                 | /plan   |
      | /\evil.example        | /driver |
      | /<TAB>/evil.example   | /driver |
      | //evil.example        | /driver |
      | https://evil.example  | /driver |
      | /plan\..\evil.example | /driver |

  Scenario: The sixth wrong password from one address is refused before it is checked
    When five wrong passwords for ann arrive from one address
    Then each of them is answered 401
    When a sixth sign-in for ann arrives from that address with the right password
    Then the site answers 429
    And no session is issued
    When ann signs in from another address with the right password
    Then a session is issued

  Scenario: A sign-in body far larger than a form is refused
    When a sign-in arrives with a 64 KB password
    Then the site answers 413
    And no session is issued

  Scenario: Signing out ends the session on every device
    Given ann is signed in on her phone and on her laptop
    When ann signs out on her phone
    Then her laptop's session no longer opens the driver page

  Scenario: A password reset ends the sessions issued before it
    Given ann is signed in on her phone and on her laptop
    When an admin resets ann's password
    Then her phone's session no longer opens the driver page
    And her laptop's session no longer opens the driver page

  Scenario: A wrong sign-in does not write the typed name to the bus
    When a wrong sign-in arrives for the name "hunter2-typed-in-the-wrong-box"
    Then the record of it carries no typed name

  Scenario: The bus is read by an admin only
    When driver ann reads "/driver/melt.json"
    Then the site answers 403
