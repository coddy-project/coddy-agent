Feature: Copying on a web UI served over plain HTTP
  A page opened at http://192.168.1.100:12345 from another machine is no secure context, so it
  has no navigator.clipboard, and the copy buttons used to do nothing in Safari on iOS. Every
  text copy control now goes through one helper: the clipboard API where the page has it,
  else a copy the page makes by itself inside the tap, from a read-only 16px text area
  selected by range. A control says Copied only for a copy that happened.

  Scenario: Code blocks, inline code and formulas copy and say Copied only when it worked
    Given a page that has no clipboard API
    Then a code block copies and says Copied only when that worked
    And inline code copies and says Copied only when that worked
    And an inline formula copies and says Copied only when that worked

  Scenario: Messages, mentions, paths and the artefact menu copy
    Given a page that has no clipboard API
    Then the copy button of a message copies
    And a skill and a mention in a sent message copy
    And the copy items of the artefact menu copy
    And the path of a changed file copies and says so only when that worked
    And Copy path of the Files window copies

  Scenario: The copy button of Settings, System says Copied only when it worked
    Given a page that has no clipboard API
    Then the System page copies the config path and says Copied only when that worked

  Scenario: The page copies as iOS needs it and no control copies on its own
    Given a page that has no clipboard API
    Then the helper copies inside the tap from a read-only 16px selection
    And no component copies text by itself
