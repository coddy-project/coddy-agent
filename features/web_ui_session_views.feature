Feature: The views of a chat are buttons in its header
  A chat has three views beside the conversation: its edits, its files and its
  background tasks. They are a row of buttons in the chat header, the way the
  views of a session sit at the top of Claude's app: Edits (only while git
  reports uncommitted changes in the chat's folder) and Files as an icon with
  a short name, Background tasks at the right edge with the dot and the
  running / total count. The tasks open in the dock beside the chat, without a
  tab strip; the edits open in a window of their own, every diff in one
  document; the files open in a window over the chat, the way the
  documentation does, with the workspace tree on the left and the files opened
  from it as tabs on the right. Over the composer of a running chat a bar
  names the repository, the branch - with a worktree mark in a linked
  worktree - and git's count of the changes, which opens the edits window.

  Scenario: The header shows the views as buttons
    Then the header shows edits, files and background tasks as buttons in a row
    And the Edits button is there only while git reports changes
    And a button opens its view
    And the button of the view on show is pressed

  Scenario: The background tasks open in the dock, the edits in a window of their own
    Then the edits open in their window, and the address names them
    And the background tasks open in the dock with no tab strip

  Scenario: The bar over the composer names where a running chat works
    Then a running chat names its repository, branch and changes over the composer
    And a linked worktree carries a worktree mark before the branch
    And before the chat starts the folder, branch and worktree are chips of the composer

  Scenario: Uncommitted changes are discarded from the edits, after a question
    Then discarding a file asks first, then puts it back through the server
    And discarding everything asks first, and a refusal touches nothing

  Scenario: The files open in a window over the chat
    Then the files open in a window over the chat, not in the dock
    And the window shows the workspace tree beside an empty preview
    And a file picked in the tree opens in a tab, and a second one beside it
    And the filter searches the whole workspace
    And a files address opens the window on its file
    And Ctrl+Shift+F opens the files window and closes it
