Feature: Accessible settings pickers in the web composer
  Scenario: Opening and changing Mode from its chip
    Then the Mode chip names the current value first and opens a radio menu that supports arrow keys, Home and End
    And committing with Enter or a click, or dismissing with Escape, returns focus to the Mode chip

  Scenario: Choosing from a short model list with the slash picker
    Then picking /model opens a radio menu, and Escape or selection returns focus to the message field

  Scenario: Choosing from a long model list with the slash picker
    Then picking /model focuses the filter in a named dialog, where Down then Enter chooses a result, and Escape or selection returns focus to the message field
