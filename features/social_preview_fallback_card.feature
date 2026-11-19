Feature: Every page carries one link-preview tag block that names a raster card

  Contract: spec-roadtrip-social-preview-cards-a-live-route-card-per-trip-the-photo-itself-on-a-photo-page-a-baked-odo-card-as-the-fallback-one-tag-block-and-a-check-that-reads-the-page-as-a-scraper-does (wave W1, the baked fallback
  card). A pasted link previews with a picture only if the card is a raster at
  an absolute https address. One partial, used by every full document, emits
  the Open Graph and Twitter tags, and only when the site's public address is
  set. With the whole site private the block says nothing about the trip.

  Scenario: A visitor's sign-in page names the fallback card in full
    Given the trip is active and the site's public address is "https://example.com"
    When a visitor reads the page "/driver/login"
    Then the page names the fallback card as an absolute https raster image
    And the card is declared 1200 by 630 as image/png with a description
    And the page carries the Twitter large-image tags
    And the tags come in the one fixed order

  Scenario: The player shell carries the same block
    Given the trip is active and the site's public address is "https://example.com"
    When the driver, signed in with the music library on, reads the page "/"
    Then the answer is the player shell
    And the page names an absolute https raster image
    And the card is declared 1200 by 630 as image/png with a description
    And the page carries the Twitter large-image tags
    And the tags come in the one fixed order

  Scenario: A private site's sign-in page names no trip
    Given the trip is active and the site's public address is "https://example.com"
    And the site's visibility is set to private
    When a visitor reads the page "/driver/login"
    Then the page names the fallback card as an absolute https raster image
    And the preview title and description do not contain the ride's name

  Scenario: A private site's pages for the driver name no trip either
    Given the trip is active and the site's public address is "https://example.com"
    And the site's visibility is set to private
    When the driver reads the page "/"
    Then the preview title and description do not contain the ride's name

  Scenario: No page names an SVG as its image
    Given the trip is active and the site's public address is "https://example.com"
    Then none of the pages "/,/plan,/photos,/driver/login" names an svg as its image

  Scenario: With no public address no preview tag is emitted
    Given the trip is active and the site has no public address
    When a visitor reads the page "/"
    Then the page carries no og or twitter tag
