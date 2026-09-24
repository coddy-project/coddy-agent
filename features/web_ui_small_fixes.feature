Feature: Five small fixes of the web UI
  Issue #368 collected five small defects of the web UI. The Russian interface
  spells a spawned agent "субагент" and keeps "worktree" in English. The usage
  limits banner is an opaque plate the chat never reads through, and a chat at
  its newest message makes room for it. The documentation reader leaves no
  empty strip on a tablet: the search and the page stretch across the sheet and
  On this page stands beside the page, or folds above it where it has no room;
  on the desktop the search ends where the text does. Its contents indent the
  pages under their group title. A read of part of a file names the lines on its
  collapsed row, the way a mention writes them. Archiving from History takes the
  row out at once, never reads the list again, and puts the row back with a note
  when the server refuses.

  Scenario: The Russian interface uses the project's words
    Then the Russian dictionary keeps the project's wording
    And the worktree chip of the Russian composer reads worktree

  Scenario: The usage banner is opaque and never hides the newest message
    Then the usage banner is an opaque plate in every theme
    And a chat at its newest message stays there when the banner rises over the composer

  Scenario: The documentation reader on a tablet and on the desktop
    Then on a tablet the documentation search and page stretch across the sheet
    And a wide tablet shows On this page beside the page
    And a narrow tablet folds On this page into a button above the page
    And on the desktop the documentation search ends where the text does
    And the pages of the contents are indented under their group title

  Scenario: A read of part of a file names its lines
    Then a read with an offset and a limit shows the lines next to the path
    And a read of the whole file shows the path alone

  Scenario: Archiving from History keeps the list where it was
    Then an archived row leaves History at once and the list is not read again
    And a refused archive puts the row back where it stood, with a note on it
    And the next page after an archive skips no conversation
