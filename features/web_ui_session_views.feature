Feature: The views of a chat
  A chat has three views beside the conversation: its files, its background
  tasks and its edits. Files and Background tasks are a row of buttons in the
  chat header, the way the views of a session sit at the top of Claude's app:
  Files as an icon with a short name, Background tasks at the right edge with
  the dot and the running / total count. The tasks open in the dock beside the
  chat, without a tab strip; the files open in a window over the chat, the way
  the documentation does, with the workspace tree on the left and the files
  opened from it as tabs on the right, each shown as its source with nothing
  over it. Over the composer of a running chat a plate joined to the top of
  the composer card names the repository, the branch - its tooltip naming the
  worktree in a linked one - and, at its right edge and only while git reports
  uncommitted changes in the chat's folder, git's count of them in a light
  frame, which opens the edits window, every diff in one document. The edits
  window is framed and headed the way the Files window is: the tree switch and
  the title on the left, a menu, the expand button and the close button on the
  right, and on the left a tree of the changed files - only those - whose rows
  scroll the diffs to their file.

  Scenario: The header shows the views as buttons
    Then the header shows files and background tasks as buttons in a row
    And the count of the edits is there only while git reports changes
    And a button opens its view
    And the button of the view on show is pressed

  Scenario: The background tasks open in the dock, the edits in a window of their own
    Then the edits open in their window, and the address names them
    And the background tasks open in the dock with no tab strip

  Scenario: The plate over the composer names where a running chat works
    Then a running chat names its repository, branch and changes over the composer
    And a linked worktree is named in the tooltip of its branch
    And before the chat starts the folder, branch and worktree are picks on the plate
    And the start screen opens on the folder and the worktree choice remembered in this browser
    And the start screen shows the branch the folder is on now

  Scenario: The edits window is framed and headed like the Files window
    Then the edits window is headed like the Files window
    And the menu of the edits holds side by side, collapse all and discard all
    And the tree of the edits lists only the changed files
    And a file picked in the tree of the edits is scrolled to and marked
    And a click beside the edits window puts it away

  Scenario: Uncommitted changes are discarded from the edits, after a question
    Then discarding a file asks first, then puts it back through the server
    And discarding everything asks first, and a refusal touches nothing

  Scenario: The files open in a window over the chat
    Then the files open in a window over the chat, not in the dock
    And the window shows the workspace tree beside an empty preview
    And a file picked in the tree opens in a tab, and a second one beside it
    And a file opens straight on its source, with no head over it
    And the filter searches the whole workspace
    And a files address opens the window on its file
    And Ctrl+Shift+F opens the files window and closes it
