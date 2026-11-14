Feature: Favourite saves what the server holds when this page has nothing loaded
  Kris, 2026-11-14: the RoadTracks card showed the last album and a Resume
  button, but Favourite said there was nothing to save, because this page had
  not loaded the album. What is playing is never kept per tab: the server holds
  one resume point per signed-in user. A Favourite or Mark that names no album
  saves that point, and the line shown says what was saved. With no resume
  point at all nothing is saved and the line says to play something first.

  Background:
    Given the trip-keeper has a clean RoadTracks card

  Scenario: Nothing loaded, a resume point held: the favourite is saved for that album and track
    Given the server holds a resume point in "Abbey Road" at track 3
    When the trip-keeper favourites with nothing loaded
    Then one favourite is saved for "Abbey Road" at track 3
    And the reply names "Abbey Road"
    And the line shown reads "Favourited: Abbey Road. See Plan."

  Scenario: Nothing loaded, a resume point held: a mark is saved at the held position
    Given the server holds a resume point in "Abbey Road" at track 3
    When the trip-keeper marks with nothing loaded
    Then one bookmark is saved for "Abbey Road" at track 3
    And the line shown reads "Marked this point: Abbey Road. See Plan."

  Scenario: Nothing loaded and no resume point: nothing is saved
    When the trip-keeper favourites with nothing loaded
    Then nothing is saved
    And the line shown reads "Play something first."

  Scenario: An album is loaded: the playing track is saved as before
    Given the server holds a resume point in "Abbey Road" at track 3
    When the trip-keeper favourites the loaded track "Something" of "Revolver" at track 5
    Then one favourite is saved for "Revolver" at track 5
    And the line shown reads "Favourited: Something. See Plan."
