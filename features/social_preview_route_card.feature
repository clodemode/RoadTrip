Feature: A public ride page names a live route card, drawn by the site, at an address that carries its version

  Contract: spec-roadtrip-social-preview-cards-a-live-route-card-per-trip-the-photo-itself-on-a-photo-page-a-baked-odo-card-as-the-fallback-one-tag-block-and-a-check-that-reads-the-page-as-a-scraper-does (wave W2, the
  trip card, look A). The card is the trip's own shape: its legs as the visitor's
  map draws them, a day disc at each night stop, the trip name, miles and days,
  and one state pill. It is a PNG of exactly 1200 by 630 that the binary draws
  at GET /og/ride/<slug>.png. The address ends in a short hash of everything the
  card shows, so a scraper that caches by address gets a new picture exactly
  when the picture changed. With the site private, or when the card cannot be
  drawn, a page names the baked fallback card and nothing about the trip.

  Scenario: A visitor's ride page names the trip card in full
    Given the trip is active and the site's public address is "https://example.com"
    When a visitor reads the page "/"
    Then the page names the trip card as an absolute https png address under "/og/ride/"
    And the card is declared 1200 by 630 as image/png with a description
    And the alt text says "The route of Alder to Elm"
    And the page carries the Twitter large-image tags

  Scenario: Every public page of the ride names the same trip card
    Given the trip is active and the site's public address is "https://example.com"
    When a visitor reads the page "/"
    Then the pages "/legs/day-1-ridge,/photos,/plan" name the same trip card as that page

  Scenario: The player shell names the same trip card
    Given the trip is active and the site's public address is "https://example.com"
    When a visitor reads the page "/"
    And the driver, signed in with the music library on, reads the page "/"
    Then the answer is the player shell
    And the page names the same trip card as the visitor's page

  Scenario: The card address answers a PNG of exactly 1200 by 630 with no session
    Given the trip is active and the site's public address is "https://example.com"
    When a visitor reads the page "/"
    And a visitor fetches the card address the page names
    Then the answer is 200 image/png and may be cached for a day
    And the bytes decode to exactly 1200 by 630 and are under 300 KB

  Scenario: The address changes when the trip's state changes and not otherwise
    Given the trip is active and the site's public address is "https://example.com"
    When a visitor reads the page "/"
    And the card address is remembered
    And a visitor reads the page "/"
    Then the card address is the remembered one
    When the car leaves the start
    And a visitor reads the page "/"
    Then the card address is not the remembered one

  Scenario: A private site names only the fallback card and does not serve the trip card
    Given the trip is active and the site's public address is "https://example.com"
    And the site's visibility is set to private
    When the driver reads the page "/"
    Then the page names the fallback card as an absolute https raster image
    And the preview title and description do not contain the ride's name
    When a visitor fetches "/og/ride/fixture-trip.png"
    Then the visitor is sent to sign in

  Scenario: The sign-in page names the fallback card
    Given the trip is active and the site's public address is "https://example.com"
    When a visitor reads the page "/driver/login"
    Then the page names the fallback card as an absolute https raster image

  Scenario: A fuzzed checkpoint never reaches the card at its raw position
    Given the trip is active and the site's public address is "https://example.com"
    Then the points handed to the card for the ride hold the snapped start and none of the raw coordinates

  Scenario: A ride that is not there has no card
    Given the trip is active and the site's public address is "https://example.com"
    When a visitor fetches "/og/ride/no-such-ride.png"
    Then the card address answers 404

  Scenario: A ride that cannot be drawn names the fallback card
    Given the trip is active and the site's public address is "https://example.com"
    And the ride's legs carry no line
    When a visitor reads the page "/"
    Then the page names the fallback card as an absolute https raster image
    When a visitor fetches "/og/ride/fixture-trip.png"
    Then the card address answers 404
