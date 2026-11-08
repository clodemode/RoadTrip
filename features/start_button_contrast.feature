Feature: Start the drive can be read on every page that shows it
  Kris, 2026-11-08: the Start the drive button had a bad contrast font. The home
  page's sky banner colours its links with the leg's accent, and that rule beat
  the button's own white text, leaving green on blue.

  Scenario: The sky banner's link colour does not reach a button
    Given the site's stylesheet
    Then the sky banner colours its links but not its buttons
