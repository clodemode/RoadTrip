Feature: A photo page names its own photo as the preview card, and every public page reads well when pasted

  Contract: spec-roadtrip-social-preview-cards-a-live-route-card-per-trip-the-photo-itself-on-a-photo-page-a-baked-odo-card-as-the-fallback-one-tag-block-and-a-check-that-reads-the-page-as-a-scraper-does (wave W3,
  the photo card and the page text). A photo page points its preview at the
  photo itself: the public picture the page shows, cropped to fill 1200 by 630,
  with a strip along the foot that names the trip and the day. The strip never
  holds a place, a checkpoint or a clock time. A photo a visitor cannot open has
  no card. With the site private the page names only the fallback card. Each
  public page also says something of its own in its title and description.

  Scenario: A visitor's photo page names its own photo card
    Given the trip is active and the site's public address is "https://example.com"
    And the trip-keeper has posted a photo on the first day
    When a visitor reads the photo page
    Then the page names the photo card as an absolute https jpeg address under "/og/photo/"
    And the photo card is declared 1200 by 630 as image/jpeg with a description
    And the alt text says "A photo from Alder to Elm, day 1"
    And the Twitter image is the same address

  Scenario: The photo card address answers a JPEG of exactly 1200 by 630 with no session
    Given the trip is active and the site's public address is "https://example.com"
    And the trip-keeper has posted a photo on the first day
    When a visitor reads the photo page
    And a visitor fetches the photo card address the page names
    Then the photo card answer is 200 image/jpeg and may be cached for a day
    And the photo card bytes decode to exactly 1200 by 630 as a jpeg and are under 300 KB

  Scenario: A photo a visitor cannot open has no card and no page names it
    Given the trip is active and the site's public address is "https://example.com"
    And the trip-keeper has posted a photo on the first day
    When a visitor reads the photo page
    And the photo card address is remembered
    And the photo is removed
    Then the photo page answers 404
    When a visitor fetches the remembered photo card address
    Then the photo card address answers 404
    And no page of the ride names a photo card

  Scenario: A removed photo that is restored has a card again
    Given the trip is active and the site's public address is "https://example.com"
    And the trip-keeper has posted a photo on the first day
    When a visitor reads the photo page
    And the photo card address is remembered
    And the photo is removed
    And the removal is undone
    And a visitor reads the photo page
    Then the photo card address is the remembered one
    When a visitor fetches the remembered photo card address
    Then the photo card answer is 200 image/jpeg and may be cached for a day

  Scenario: A photo on a ride the site is not showing has no card
    Given the trip is active and the site's public address is "https://example.com"
    And the trip-keeper has posted a photo on the first day
    And the same photo is on a post of another ride
    When a visitor fetches the photo card of that post
    Then the photo card address answers 404

  Scenario: A private site names only the fallback card and does not serve the photo card
    Given the trip is active and the site's public address is "https://example.com"
    And the trip-keeper has posted a photo on the first day
    And the site's visibility is set to private
    When the driver reads the photo page
    Then the page names the fallback card as an absolute https raster image
    And the preview title and description do not contain the ride's name
    When a visitor fetches the photo card of the photo
    Then the photo card request is sent to sign in

  Scenario: The strip names the trip and the day and never a place or a time
    Given the trip is active and the site's public address is "https://example.com"
    And the trip-keeper has posted a photo on the first day
    Then the strip handed to the drawing code reads "Alder to Elm" over "Day 1"
    And the strip adds no checkpoint name, no place and no clock time to the trip's name

  Scenario: Two photos have two card addresses and one photo keeps its own
    Given the trip is active and the site's public address is "https://example.com"
    And the trip-keeper has posted a photo on the first day
    And the trip-keeper has posted a second, different photo on the first day
    When the card addresses of both photo pages are read
    Then the two card addresses differ
    And reading the first photo page again names the same address

  Scenario: A photo that cannot be drawn names the ride's trip card
    Given the trip is active and the site's public address is "https://example.com"
    And the trip-keeper has posted a photo on the first day
    And the photo's file is not a picture
    When a visitor reads the photo page
    Then the page names the trip card as an absolute https png address under "/og/ride/"

  Scenario: Every public page says something of its own
    Given the trip is active and the site's public address is "https://example.com"
    And the trip-keeper has posted a photo on the first day
    Then the page "/" has its own preview title and description
    And the page "/legs/day-1-ridge" has its own preview title and description
    And the page "/photos" has its own preview title and description
    And the page "/plan" has its own preview title and description
    And the photo page has its own preview title and description

  Scenario: A photo page describes itself in the post's own public words, cut cleanly
    Given the trip is active and the site's public address is "https://example.com"
    And the trip-keeper has posted a photo on the first day with a long note
    When a visitor reads the photo page
    Then the preview description is the note cut at a word with an ellipsis and under 160 characters
