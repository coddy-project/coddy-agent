Feature: The views of a chat are buttons in its header
  A chat has three views beside the conversation: its edits, its files and its
  background tasks. They are a row of buttons in the chat header, the way the
  views of a session sit at the top of Claude's app: Edits (only while the
  session has edits) and Files as an icon with a short name, Background tasks
  at the right edge with the dot and the running / total count. The tasks and the edits
  open in the dock beside the chat, one at a time and without a tab strip of
  their own; the files open in a window over the chat, the way the
  documentation does, with the workspace tree on the left and the files opened
  from it as tabs on the right.

  Scenario: The header shows the views as buttons
    Then the header shows edits, files and background tasks as buttons in a row
    And the Edits button is there only while the session has edits
    And a button opens its view
    And the button of the view on show is pressed

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
