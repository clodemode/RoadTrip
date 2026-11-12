Feature: Chester finds a podcast episode that fits the place and shows it as a card
  Kris, 2026-11-12: Chester can search Apple's free podcast search (no key) and
  recommend one episode as a card whose button opens it in Apple Podcasts. The
  server drops clips and marathons (under 15 minutes or over 2 hours) and any
  episode whose link is not on apple.com, so the model chooses only from
  episodes worth a drive. The model never sees an address: it names an episode
  by a short reference from this question's own search and the server builds
  the card. The search is told words only, never a coordinate.

  Background:
    Given the podcast search holds the episode "Valley Voices" "How the TVA changed the river" of 40 minutes at "https://podcasts.apple.com/us/podcast/tva/id1?i=100"

  Scenario: A 5 minute episode is dropped
    Given the podcast search also holds the episode "Quick Facts" "TVA in a flash" of 5 minutes at "https://podcasts.apple.com/us/podcast/flash/id2?i=101"
    And Chester will search podcasts for "Tennessee Valley Authority history"
    And Chester will then answer "Here is one."
    When the traveller asks Chester for "a podcast about the TVA"
    Then Chester was told about "How the TVA changed the river"
    And Chester was not told about "TVA in a flash"

  Scenario: A 250 minute episode is dropped
    Given the podcast search also holds the episode "Sleep Tracks" "TVA all night" of 250 minutes at "https://podcasts.apple.com/us/podcast/night/id3?i=102"
    And Chester will search podcasts for "Tennessee Valley Authority history"
    And Chester will then answer "Here is one."
    When the traveller asks Chester for "a podcast about the TVA"
    Then Chester was told about "How the TVA changed the river"
    And Chester was not told about "TVA all night"

  Scenario: show_podcast puts one card with show, episode, minutes and the Apple link
    Given Chester will search podcasts for "Tennessee Valley Authority history"
    And Chester will then show the podcast "p1" because "It is about the river this road follows."
    And Chester will then answer "Tap the button to open it in Apple Podcasts."
    When the traveller asks Chester for "a podcast about the TVA"
    Then the answer carries one podcast card
    And the card names the show "Valley Voices" and the episode "How the TVA changed the river"
    And the card says 40 minutes and "It is about the river this road follows."
    And the card's button is "Open in Apple Podcasts" and opens "https://podcasts.apple.com/us/podcast/tva/id1?i=100"
    And the podcast answer was written after the last tool call
    And the page builds the button as a new-tab link

  Scenario: A result whose link is not on apple.com is dropped
    Given the podcast search also holds the episode "Elsewhere" "A lookalike link" of 45 minutes at "https://podcasts.apple.com.evil.example/x"
    And the podcast search also holds the episode "Elsewhere" "A plain http link" of 45 minutes at "http://podcasts.apple.com/x"
    And Chester will search podcasts for "Tennessee Valley Authority history"
    And Chester will then answer "Here is one."
    When the traveller asks Chester for "a podcast about the TVA"
    Then Chester was told about "How the TVA changed the river"
    And Chester was not told about "A lookalike link"
    And Chester was not told about "A plain http link"

  Scenario: The search request carries no coordinate
    Given Chester will search podcasts for "Shenandoah Valley history 38.5123, -78.4412 along 38.5123,-78.4412"
    And Chester will then answer "Here is one."
    When the traveller asks Chester for "a podcast about here"
    Then the search service was asked for podcast episodes, limit 25, with the words "Shenandoah Valley history along"
    And no digit follows a point in what the search service was sent

  Scenario: The preset is on the Chester page and offers the podcast tools
    When the traveller opens the Chester page
    Then the page has a preset button "A podcast for this stretch"
    When the traveller taps the preset "A podcast for this stretch"
    Then the API was asked about the country the car is driving through, in the next two hours
    And Chester was offered the tools "search_podcasts" and "show_podcast"

  Scenario: A typed request can use the podcast tools too
    Given Chester will answer "Fuel is ahead."
    When the traveller asks Chester for "Where is the next fuel?"
    Then Chester was offered the tools "search_podcasts" and "show_podcast"

  Scenario: The search service failing gives a plain sentence and no card
    Given the podcast search is down
    And Chester will search podcasts for "Tennessee Valley Authority history"
    And Chester will then answer "The podcast search is unavailable right now."
    When the traveller asks Chester for "a podcast about the TVA"
    Then Chester was told the podcast search is unavailable
    And the answer says the podcast search is unavailable
    And the answer carries no podcast card

  Scenario: The card is on the logged row
    Given Chester will search podcasts for "Tennessee Valley Authority history"
    And Chester will then show the podcast "p1" because "It is about the river this road follows."
    And Chester will then answer "Tap the button to open it."
    And the bus is listening
    When the traveller asks Chester for "a podcast about the TVA"
    Then the logged row holds the podcast card for "How the TVA changed the river"
    And the bus carries a chester_podcast_search record with the count 1 and not the query
