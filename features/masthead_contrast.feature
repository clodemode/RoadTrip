Feature: Every control on the masthead and in the stop row can be read
  Kris, 2026-11-18, on the road: the words and the icon on the Drop Pin button
  were hard to see. Contrast was proved for that one button; every other
  control was unmeasured. This feature computes the contrast of each control
  from the stylesheet itself (the band is the masthead's whole gradient, so a
  control is judged on its worst stop) and refuses a control that is not on the
  list, so a new masthead control cannot ship unmeasured.

  Scenario Outline: A masthead control reads at 7 to 1 or better on the whole band
    Given the stylesheet is read
    Then the "<control>" rule "<selector>" reads at 7 to 1 or better, taking its ink from "<ink from>" when it sets none

    Examples:
      | control                   | selector                                | ink from  |
      | brand title               | .mast-title                             | .masthead |
      | menu link                 | .mast-links a                           | .masthead |
      | menu link on hover        | .mast-links a:hover                     | .masthead |
      | current page link         | .mast-links a[aria-current="page"]      | .masthead |
      | Sign out                  | .mast-links .linkish                    | .masthead |
      | Sign out on hover         | .mast-links .linkish:hover              | .masthead |
      | Built with CLODE badge    | .clode-badge                            | .masthead |
      | badge on hover            | .clode-badge:hover                      | .masthead |
      | Drop Pin                  | .mast-links .drop-pin                   | .masthead |
      | Drop Pin on hover         | .mast-links .drop-pin:hover             | .masthead |

  Scenario Outline: A stop row button reads at 7 to 1 or better
    Given the stylesheet is read
    Then the "<control>" rule "<selector>" reads at 7 to 1 or better, taking its ink from "<ink from>" when it sets none

    Examples:
      | control                | selector                                  | ink from        |
      | stop row button        | .stop-row .stop-btn                       | .stop-row .stop-btn |
      | stop row button hover  | .stop-row .stop-btn:hover                 | .stop-row .stop-btn |
      | stop row disabled      | .stop-row .stop-btn[aria-disabled="true"] | .stop-row .stop-btn |

  Scenario: Every masthead control is on the contrast list
    Given the masthead's template
    Then every link and button in the masthead is one the contrast list names
