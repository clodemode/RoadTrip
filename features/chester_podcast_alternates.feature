Feature: Chester shows a podcast pick and up to two alternates, each its own card
  Kris, 2026-11-14, on v55: Chester showed only one episode. show_podcast now
  takes one to three episodes, the best pick first and then alternates, and the
  page shows each as its own card with Open in Apple Podcasts. The 5 to 30
  minute window and the apple.com link rule are unchanged, and the server still
  builds every card from its own search, so an unknown reference is dropped.

  Background:
    Given the podcast search holds the episode "Valley Voices" "How the TVA changed the river" of 20 minutes at "https://podcasts.apple.com/us/podcast/tva/id1?i=100"
    And the podcast search also holds the episode "Park Radio" "A ranger on the ridge" of 12 minutes at "https://podcasts.apple.com/us/podcast/ridge/id2?i=101"
    And the podcast search also holds the episode "Old Roads" "The turnpike years" of 28 minutes at "https://podcasts.apple.com/us/podcast/turnpike/id3?i=102"
    And the podcast search also holds the episode "Late Hour" "Fourth in the list" of 15 minutes at "https://podcasts.apple.com/us/podcast/fourth/id4?i=103"

  Scenario: Three references become three cards in the order given
    Given Chester will search podcasts for "Tennessee Valley Authority history"
    And Chester will then show the podcasts "p1" because "The river this road follows." and "p2" because "Short and local." and "p3" because "The road itself."
    And Chester will then answer "The first is my pick; the others are alternates."
    When the traveller asks Chester for "a podcast about the TVA"
    Then the answer carries the podcast cards in this order: "How the TVA changed the river", "A ranger on the ridge", "The turnpike years"

  Scenario: A reference the search never returned is dropped
    Given Chester will search podcasts for "Tennessee Valley Authority history"
    And Chester will then show the podcasts "p1" because "The river this road follows." and "p9" because "Not a real reference."
    And Chester will then answer "Here is one."
    When the traveller asks Chester for "a podcast about the TVA"
    Then the answer carries the podcast cards in this order: "How the TVA changed the river"

  Scenario: A fourth episode is refused by the schema and never becomes a card
    Given Chester will search podcasts for "Tennessee Valley Authority history"
    And Chester will then show the podcasts "p1" because "One." and "p2" because "Two." and "p3" because "Three." and "p4" because "Four."
    And Chester will then answer "Here are three."
    When the traveller asks Chester for "a podcast about the TVA"
    Then the show_podcast tool refuses a fourth episode in its schema
    And the answer carries no card for "Fourth in the list"
