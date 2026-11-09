Feature: Opening Coddy as a desktop window
  `coddy desktop` runs the HTTP surface on a loopback port this machine
  remembers and opens a window on the tab shell. The port is remembered rather
  than taken fresh each time because the origin includes it, and the shell
  keeps its tabs where the browser keeps them per origin. The strip's close
  button ends the run unless a visible tray keeps it alive; starting the app
  again while a window is open returns to that window instead of serving a
  second time.

  Scenario: The window opens on a port the desktop writes down
    Given a machine where Coddy has never opened a desktop window
    When the operator opens the desktop
    Then the HTTP surface runs on a loopback port of its own
    And that port is written down for the next launch
    And the browser is opened on the tab shell of that port, in a profile of its own

  Scenario: The next launch comes back to the same port
    Given a machine where Coddy has opened a desktop window before
    When the operator opens the desktop
    Then the HTTP surface runs on the port of the last launch

  Scenario: Closing the window ends the run
    Given a desktop window open on its own Coddy
    When the window closes
    Then the run ends and the HTTP surface stops with it

  Scenario: Opening the desktop again returns to the window already open
    Given a desktop window open on its own Coddy
    When the operator opens the desktop again
    Then the second run serves nothing of its own
    And it opens the tab shell of the desktop already running

  Scenario: Only the HTTP surface runs behind the window
    Given a configuration that also enables the swarm relay and the scheduler
    When the desktop decides what to run
    Then it names the surfaces it left to `coddy serve`
    And it changes nothing in the configuration itself

  Scenario: A chord the browser answers itself still reaches the strip
    Given a window whose browser answers Ctrl+T and Ctrl+W itself
    When the operator presses Ctrl+T
    Then the strip is told to open a chat
    When the operator presses Ctrl+W
    Then the strip is told to close the chat in front
    When the operator presses Ctrl+Tab
    Then the strip is told to go to the next chat
    When the operator presses Ctrl+Shift+Tab
    Then the strip is told to go to the previous chat
    When the operator presses Ctrl+R
    Then the strip is told to reload the chat in front

  Scenario: The tray icon offers the menu of the bar, and the window above it
    Given a desktop window whose tray icon carries a menu
    Then the tray's menu offers every row the bar's menu offers
    And above them it offers showing the window and hiding it
    When the operator picks "Show the window" from the tray
    Then the window is brought back
    When the operator picks "Hide the window" from the tray
    Then the window is put away
    When the operator picks "Open at login" from the tray
    Then the switch is written down for the next launch
    And the bar's menu draws it ticked
    When the operator picks "New chat" from the tray
    Then the strip is told to open a chat
    When the operator picks "Quit Coddy" from the tray
    Then the window is closed

  Scenario: Closing the shell minimises to a visible tray
    Given a desktop window with a visible tray icon
    When the operator closes the window from the shell
    Then the window is minimised
    And the window is not closed

  Scenario: Closing the shell without a tray ends the window
    Given a desktop window without a tray icon
    When the operator closes the window from the shell
    Then the window is closed once
    And the window is not minimised

  Scenario: A link out of Coddy opens in this machine's own browser
    Given a window on the tab shell
    When a chat asks the window to open "https://coddy.dev/docs/surfaces-desktop"
    Then this machine's own browser is opened on it
    When a chat asks the window to open "file:///etc/shadow"
    Then the window opens nothing

  Scenario: A row of the application menu opens a chat in its folder
    Given a window on the tab shell
    When the application menu asks for a chat in a folder of this machine
    Then a chat is opened in that folder
    When the application menu asks for a chat in a folder that is not there
    Then the window refuses, and the strip is told nothing

  Scenario: The application menu of this user names Coddy and its recent folders
    Given a machine where Coddy has never opened a desktop window
    And two chats of this home were had in folders of their own
    When the operator opens the desktop
    Then this user's application entry opens Coddy and names the window's class
    And it offers both folders, each opening a chat of its own

  Scenario: A drag moves the window only once it knows where the drag began
    Given a window on the tab shell
    When the page asks to move the window before saying where the drag began
    Then the window does not move
    When the drag begins and the page asks again
    Then the window follows the pointer

  Scenario: A window the desktop pinned is let loose before it is dragged
    Given a window the window manager has pinned to the left half of the screen
    When the bar is grabbed three quarters along it and dragged
    Then the window is let loose before it follows the pointer
    And it is put back under the pointer, three quarters along its own width

  Scenario: A drag to the left edge keeps going
    Given a window being dragged by its bar
    When the pointer carries it past the left edge of the screen
    Then the window follows it off the edge
    But a drag never puts the bar above the top of the screen

