Feature: A bad photo is removed and its post stays as a marker with the same place and time
  Kris, 2026-11-14: many early photos on the trip were bad. The trip-keeper can
  remove the photo from a post and keep the post as a plain marker: the same
  occurred_at, the same zone, the same pin (still only the published point, never
  a raw one). One tap on "Remove photo", no dialog, and an undo window of ten
  minutes. For that window the original and its thumbnail are not under the
  public media path: they wait in a holding folder on the volume that no page
  serves, and the exact point the photo carried waits with them. When the window
  closes they are deleted and Undo is gone. The post is never deleted. A
  signed-out visitor cannot remove a photo. The /driver/posts page lists the
  ride's photo posts with a checkbox each, so many photos can be removed at once.

  Background:
    Given the trip is active and kris is signed in
    And the ride has two photo posts "Bad one" and "Bad two", each pinned and held on the public media path

  Scenario: Removing a photo keeps the post with its time and pin and no image
    When kris taps Remove photo on the post "Bad one"
    Then the post "Bad one" keeps the post with the same time, zone and pin
    And the post "Bad one" has no photo
    And the photo's image address answers 404
    And the photo's thumbnail address answers 404
    And the post "Bad two" still has its photo

  Scenario: A visitor sees the marker where the photo was
    When kris taps Remove photo on the post "Bad one"
    Then a visitor sees the post "Bad one" on the home page with no picture
    And the ride's map data still has a point for the post "Bad one" at the same place

  Scenario: Nothing is left under the public path and the original waits out of sight
    When kris taps Remove photo on the post "Bad one"
    Then no file of that photo is under the public media path
    And the original waits in a holding folder that no page serves

  Scenario: A signed-out visitor cannot remove a photo
    When a visitor who is not signed in posts Remove photo for the post "Bad one"
    Then the visitor is refused
    And the post "Bad one" still has its photo

  Scenario: Undo inside the window brings the photo back
    Given kris taps Remove photo on the post "Bad one"
    And the photo's image address answers 404
    When kris taps Undo on the post "Bad one" 5 minutes later
    Then the post "Bad one" has its photo again
    And the photo's image address answers 200

  Scenario: When the window closes the files are gone and Undo is refused
    Given kris taps Remove photo on the post "Bad one"
    When 11 minutes pass and the holding folder is swept
    Then nothing of that photo is anywhere on the volume
    And kris taps Undo on the post "Bad one" and is told there is nothing left to undo
    And the post "Bad one" has no photo

  Scenario: Many photos can be removed at once from /driver/posts
    When kris opens /driver/posts
    Then the page lists both photo posts with a checkbox each and no dialog
    When kris ticks both and taps Remove photos
    Then the post "Bad one" has no photo
    And the post "Bad two" has no photo
    And both posts are still on the ride
