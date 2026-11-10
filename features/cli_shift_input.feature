Feature: Typing with Shift in the console
  Printable characters encoded by the terminal remain text while Shift is held.

  Scenario: Continue typing before releasing Shift
    Given an empty console editor receiving terminal input
    When the terminal sends "modifyOtherKeys" characters while Shift stays held
    Then the console draft is "AB! C"

  Scenario: Continue typing with Kitty key reports
    Given an empty console editor receiving terminal input
    When the terminal sends "Kitty" characters while Shift stays held
    Then the console draft is "AB! C"

  Scenario Outline: Correct the first letter after a space without releasing Shift
    Given an empty console editor receiving terminal input
    When the terminal corrects a letter using "<protocol>" while Shift stays held
    Then the console draft is "hello B"

    Examples:
      | protocol        |
      | modifyOtherKeys |
      | Kitty           |
