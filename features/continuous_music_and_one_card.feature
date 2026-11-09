Feature: Music plays on between screens, in one Drive Tracks card, with the next songs already downloaded
  Kris, 2026-11-09: music should NOT pause, but play continuously between
  screens unless the album ends, the user signs out, or presses Pause. The
  Drive Tracks card is one full-width card whose title is its header, and it
  downloads songs in advance (MusicOrgan's NESTING).

  Every page was a full page load, which destroys the one audio element. So
  while the ride is Active a signed-in user's page is a player shell: the card,
  the one audio element and one iframe that holds the real page. Every link and
  form inside the frame stays in the frame, so nothing unloads the audio.
  This replaces the one-tap Resume on each page of the menu feature.

  Background:
    Given a continuous-music trip with a music library
    And the continuous-music trip-keeper Kris is signed in

  # --- the shell, on the server ------------------------------------------------

  Scenario Outline: A signed-in page while Active is the player shell
    When the continuous-music drive is started
    And Kris opens the page "<path>"
    Then the page is the player shell with exactly one audio element and one frame
    And the frame shows "<path>" as an embedded page
    And no element id appears twice in the shell

    Examples:
      | path              |
      | /                 |
      | /plan             |
      | /photos           |
      | /chester          |
      | /driver           |
      | /driver/post      |
      | /driver/lodging   |
      | /legs/day-1-ridge |

  Scenario: The shell frame keeps the page's own query
    When the continuous-music drive is started
    And Kris opens the page "/driver/post?error=Try+again"
    Then the frame shows "/driver/post?error=Try+again" as an embedded page

  Scenario Outline: An embedded page is a normal page without the card or the audio
    When the continuous-music drive is started
    And Kris opens the page "<path>?embed=1"
    Then the page keeps its banner, menu, breadcrumb and one h1
    And the page has no audio element, no Drive Tracks card and no frame
    And the page carries the frame script

    Examples:
      | path              |
      | /                 |
      | /plan             |
      | /driver           |
      | /driver/post      |
      | /legs/day-1-ridge |

  Scenario: A navigation made inside a frame is embedded without being told so
    When the continuous-music drive is started
    And Kris opens the page "/plan" as a frame navigation
    Then the page keeps its banner, menu, breadcrumb and one h1
    And the page has no audio element, no Drive Tracks card and no frame

  Scenario: A redirect after posting from the frame lands in the frame
    When the continuous-music drive is started
    And Kris posts an empty post form from the frame
    Then the answer redirects to a path that carries embed=1

  Scenario: A redirect after posting from a whole page is left alone
    When the continuous-music drive is started
    And Kris posts an empty post form from a whole page
    Then the answer redirects to a path that does not carry embed=1

  Scenario: Signing out answers with a plain redirect home, never an embedded one
    When Kris signs out
    Then the answer redirects to "/" with no embed=1

  Scenario: A visitor gets no shell
    When the continuous-music drive is started
    And a visitor opens the page "/"
    Then the page has no player shell, no audio element and no frame

  Scenario Outline: Planning gets no shell
    Given the drive has not started
    When Kris opens the page "<path>"
    Then the page has no player shell and no frame

    Examples:
      | path         |
      | /            |
      | /plan        |
      | /driver      |
      | /driver/post |

  Scenario: Without a music library there is nothing to keep playing and no shell
    Given the music library is off
    When the continuous-music drive is started
    And Kris opens the page "/plan"
    Then the page has no player shell and no frame

  Scenario: Only this site may frame a page
    When the continuous-music drive is started
    Then every kind of answer says it may be framed by its own origin only

  Scenario: Sign out is the one thing that leaves the frame
    When the continuous-music drive is started
    And Kris opens the page "/plan?embed=1"
    Then the Sign out form targets the top window

  Scenario: A session that ends inside the frame breaks out to the sign-in page
    When the continuous-music drive is started
    And a visitor opens the page "/driver?embed=1"
    Then the answer redirects to the sign-in page whose next is "/driver"
    And the sign-in page asked for from the frame carries the break-out

  # --- one card ---------------------------------------------------------------

  Scenario: One card, its title is its header, and Chester's album is a labelled action in it
    When the continuous-music drive is started
    And Kris opens the page "/plan"
    Then the shell has one Drive Tracks card with a heading, art, title, artist, track, Play and Next
    And Ask Chester for an album is a labelled button inside that card with its icon showing
    And there is no second Drive Tracks card or strip
    And the card says how many songs are ready
    And the card's controls are at least 44 px tall and the card is docked full width

  Scenario: Reload offers Resume inside the same card
    When the continuous-music drive is started
    And Kris has a continuous-music resume point for "Abbey Road" at 42 seconds
    And Kris opens the page "/plan"
    Then the card offers a Resume of "Abbey Road" at 42 seconds

  # --- the player, under node ---------------------------------------------------

  Scenario: The frame loading a new page does not touch the audio
    Given an album of 4 tracks is playing in the shell
    When the frame loads "/plan?embed=1"
    And the frame loads "/photos?embed=1"
    Then the frame loading a new page does not touch the audio
    And the address bar reads "/photos" without embed=1
    And the tab title is the frame page's title

  Scenario: A page that carries a query and a fragment keeps both in the address bar
    Given an album of 4 tracks is playing in the shell
    When the frame loads "/driver/post?error=x&embed=1#post-9"
    Then the address bar reads "/driver/post?error=x#post-9"

  Scenario: A tap inside the frame starts the shell's player and unlocks sound first
    Given the shell is idle
    When a tap in the frame starts album 42
    Then sound is unlocked in the tap before the album is asked for
    And the shell's audio element is playing

  Scenario: The album ending is a stop
    Given an album of 2 tracks is playing in the shell
    When the last track ends
    Then the audio is stopped and the card says "Album finished"

  Scenario: A track ending mid-album plays on
    Given an album of 4 tracks is playing in the shell
    When the first track ends
    Then the audio is playing track 2

  Scenario: Pause is a stop
    Given an album of 4 tracks is playing in the shell
    When the listener presses Pause
    Then the audio is stopped and the place is saved

  Scenario: Signing out is a stop
    Given an album of 4 tracks is playing in the shell
    When the listener signs out
    Then the audio is stopped and the place is saved

  Scenario: Navigating, then a stop from the lock screen, is still just a pause
    Given an album of 4 tracks is playing in the shell
    When the frame loads "/chester?embed=1"
    And the listener presses Pause
    Then the audio is stopped and the place is saved

  # --- the frame script, under node ----------------------------------------------

  Scenario: Links that leave the app open outside the frame, links inside it stay
    Given the frame script runs on a page of this site
    Then a link to "https://waze.com/ul?ll=1,2" opens a new tab
    And a link to "/plan" stays in the frame and carries embed=1
    And a link to "/photos?x=1#top" stays in the frame and carries embed=1
    And a link to "#post-3" is left alone
    And a link to "/chester" that opens a new tab is left alone
    And a link to "tel:5551234" opens in the top window

  Scenario: Forms in the frame keep embed=1 and sign out goes to the top
    Given the frame script runs on a page of this site
    Then a post form to "/driver/post" is sent with embed=1
    And a search form to "/photos" is sent with embed=1
    And the sign out form stops the music and is left to go to the top window

  # --- downloading songs in advance (NESTING), under node ------------------------

  Scenario: Songs are downloaded one at a time, current first, then ahead in order, up to the limit
    Given an album of 6 tracks is playing in the shell
    Then the songs downloaded were tracks 1, 2, 3 and 4 in that order
    And no two downloads ran at once
    And the card says 3 ready

  Scenario: A downloaded track plays from memory with no network
    Given an album of 6 tracks is playing in the shell
    When the first track ends
    Then the audio plays track 2 from its downloaded copy
    And playing it asked the network for nothing
    And the downloaded copy keeps the content type "audio/ogg; codecs=opus"

  Scenario: A track not yet downloaded plays from its ordinary address
    Given the shell has not finished downloading
    When an album of 6 tracks is started
    Then the audio plays track 1 from its ordinary address

  Scenario: A download that fails leaves nesting cold and playback untouched
    Given the library's storage refuses cross-origin downloads
    When an album of 6 tracks is started
    Then nothing was downloaded
    And the audio plays track 1 from its ordinary address
    And exactly one log line says nesting is cold
    And the card never shows a playback error
    And the first track ending still plays track 2 from its ordinary address

  Scenario: An expired address is signed again during the download, silently
    Given the library's first addresses have expired
    When an album of 6 tracks is started
    Then the album was asked for again once
    And the songs downloaded were tracks 1, 2, 3 and 4 in that order
    And the card never shows a playback error

  Scenario: Over the memory ceiling the farthest songs are let go first
    Given the memory ceiling is 2500 bytes and each song is 1000 bytes
    And an album of 8 tracks is playing in the shell
    When the first track ends
    And the first track ends
    Then at most 2 songs are held
    And the songs let go were the farthest from the playing track, each released
    And the playing track is never let go

  Scenario: A new album lets the old album's songs go
    Given an album of 6 tracks is playing in the shell
    When another album is started
    Then every held song of the old album was released

  Scenario: Downloaded songs are kept in memory only
    Then the player never writes a song to disk, a database or a cache and offers no save

  Scenario: The card collapses to its header so the site stays visible on a phone
    # Kris, 2026-11-09: it "takes up too much space and hides the rest of the
    # site on a phone. Use the Charter icon from MusicOrgan."
    Then the card opens collapsed with a header that expands it and the Charter icon
