Feature: Every answer carries the transport and content headers, and the player shell still frames the site
  Audit, 2026-11-16: the site served a frame policy and nothing else. Every
  answer now also tells the browser to stay on https, not to guess a content
  type, and not to send the full address to another site. The frame policy
  stays same-origin, because the player shell frames the site's own pages.

  Scenario Outline: The headers are on a page, the sign-in page and a media answer
    When a visitor asks for "<address>"
    Then the answer says to stay on https for a year
    And the answer says not to guess the content type
    And the answer keeps the full address from other sites
    And the answer may be framed by this site only

    Examples:
      | address             |
      | /                   |
      | /driver/login       |
      | /media/2026/11/x.jpg |

  Scenario: A page asked for by the player shell's frame still renders and may be framed
    When the player shell's frame asks for "/"
    Then the site answers 200
    And the answer may be framed by this site only

  Scenario Outline: A folder or a dot-file is never listed or served
    Given a file ".upload-123" is sitting in the media folder
    When a visitor asks for "<address>"
    Then the site answers 404

    Examples:
      | address            |
      | /static/           |
      | /static/js/        |
      | /media/            |
      | /media/.upload-123 |
