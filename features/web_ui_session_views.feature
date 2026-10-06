Feature: The views of a chat are picked from its header menu
  A chat has three views beside the conversation: its background tasks, its
  edits and its files. The control at the right of the chat header opens a menu
  of the three, the way the views of a session are picked in Claude's app. The
  tasks and the edits open in the dock beside the chat, one at a time and
  without a tab strip of their own; the files open in a window over the chat,
  the way the documentation does, with the workspace tree on the left and the
  files opened from it as tabs on the right.

  Scenario: The header menu offers the three views
    Then the header control opens a menu of background tasks, edits and files
    And picking a view opens it and puts the menu away
    And the view on show is checked in the menu

  Scenario: The edits and the background tasks open in the dock, without a tab strip
    Then the edits open in the dock headed Edits, with no tab strip
    And the background tasks open in the dock with no tab strip

  Scenario: The files open in a window over the chat
    Then the files open in a window over the chat, not in the dock
    And the window shows the workspace tree beside an empty preview
    And a file picked in the tree opens in a tab, and a second one beside it
    And the filter searches the whole workspace
    And a files address opens the window on its file
    And Ctrl+Shift+F opens the files window and closes it
