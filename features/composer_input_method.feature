Feature: Typing through an input method in the web composer
  A Japanese, Chinese or Korean input method turns keystrokes into candidates
  and confirms one with Enter. Its keydowns carry isComposing, except in
  Safari, which ends the composition first and marks the key that ended it,
  a few milliseconds later, only with keyCode 229. Those keys belong to the input method: the composer
  sends nothing on them, and none of its pickers takes a row, moves its
  highlight or closes, so the candidate lands in the draft as the input method
  chose it. A keyCode 229 with no composition behind it, which Android
  keyboards send for ordinary keys, stays an ordinary key.

  Scenario: Keys the input method is composing with stay with it
    Then Enter that confirms an input-method candidate does not send the draft
    And the slash picker takes no row and keeps its highlight on keys the input method is composing with
    And the @ picker takes no row and keeps its highlight on keys the input method is composing with
    And the /compact option picker takes no row and keeps its highlight on keys the input method is composing with
    And the line-range picker stays open on an Escape the input method is composing with
    And a keyCode 229 with no composition behind it still takes a row of the @ picker
