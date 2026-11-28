Feature: Accessible settings pickers in the web composer
  Scenario: Opening and changing Mode from its chip
    Then the Mode chip names the current value first and opens a radio menu that supports arrow keys, Home and End
    And committing with Enter or a click, or dismissing with Escape, returns focus to the Mode chip

  Scenario: Choosing from a short model list with the slash picker
    Then picking /model opens a radio menu, and Escape or selection returns focus to the message field

  Scenario: Identifying model vendors in a short menu
    Then the short model menu exposes named vendor groups with radio choices

  Scenario: Choosing from a long model list with the slash picker
    Then picking /model focuses the filter in a named dialog, where Down then Enter chooses a result, and Escape or selection returns focus to the message field

  Scenario: Opening filtered pickers with ArrowUp
    Then opening the filtered model picker with ArrowUp focuses its last result
    And opening the branch picker with ArrowUp focuses its last option

  Scenario: Choosing a workspace branch with the keyboard
    Then the branch picker moves through options without selecting until Enter and returns focus to its chip

  Scenario: Navigating the folder browser with the keyboard
    Then opening the folder browser exposes a named dialog and traps keyboard focus within it
    And pressing Escape closes the folder browser and returns focus to the workspace chip
