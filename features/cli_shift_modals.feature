Feature: Shift input in console overlays
  The console's search fields and letter commands accept encoded terminal input.

  Scenario Outline: Correct text in a search field without releasing Shift
    Given the console "<surface>" overlay
    When encoded "<protocol>" input types a letter, corrects it and adds punctuation
    Then the overlay search text is "Ж!"

    Examples:
      | surface     | protocol        |
      | selector    | modifyOtherKeys |
      | selector    | Kitty           |
      | docs search | modifyOtherKeys |
      | docs search | Kitty           |

  Scenario Outline: Use uppercase letter commands in an overlay
    Given the console "<surface>" overlay
    When encoded "<protocol>" input runs the overlay's letter commands
    Then the overlay commands have taken effect

    Examples:
      | surface   | protocol        |
      | tasks     | modifyOtherKeys |
      | tasks     | Kitty           |
      | docs page | modifyOtherKeys |
      | docs page | Kitty           |
