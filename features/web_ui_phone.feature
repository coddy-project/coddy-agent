Feature: The web UI on a phone
  A phone gives the web UI 360 to 430 CSS pixels and a keyboard without
  Shift+Enter. The phone is a tier of the layout grid - phone up to 599px,
  tablet up to 1199px, desktop, wide from 1920px - and every width the
  stylesheet and the code ask about is an edge of that grid or a threshold it
  lists. What Enter does in the composer follows the input device, not the
  width of the window: wherever a keyboard is attached, a narrow desktop window
  included, Enter sends and Shift+Enter or Ctrl+Enter starts a new line; on a
  touch-only phone Return stays a newline and the Send button sends. The layout
  gives a phone one-line chip strips that scroll sideways beside the controls
  that must stay put, a top bar whose icons never cover the brand and fold
  behind a More button when they do not fit, a start screen and a transcript
  that never widen the page, settings tiles that spell their whole name, and
  text fields large enough that iOS Safari does not zoom into them. Tablets and
  desktops keep their layout.

  Scenario: Enter sends from a keyboard, whatever the width of the window
    Then in a narrow desktop window Enter sends the draft
    And Shift+Enter leaves the newline to the browser
    And Ctrl+Enter inserts a newline at the caret instead of sending

  Scenario: On a touch-only phone Return is a newline and the button sends
    Then on a touch-only phone Return inserts a newline and the Send button sends
    And the on-screen keyboard labels its Enter key send, or enter on a touch-only phone

  Scenario: The composer fits a phone
    Then the selector chips scroll sideways in one strip and never run under Send
    And the context chips scroll sideways in one strip beside the improve-prompt button
    And the composer text is large enough that iOS Safari does not zoom into it

  Scenario: The top bar and the start screen fit a phone
    Then the brand gives way and the top bar icons never slide over it
    And the start screen never widens the page

  Scenario: What the top bar has no room for is behind More
    Then a phone bar short of room keeps History, folds the rest behind More and lists sign-out last
    And picking a folded item opens it and closes the menu

  Scenario: The phone is a tier of the layout grid
    Then the phone tier ends at 599px and the tablet tier starts at 600px
    And every width the stylesheet asks about is an edge of the grid or a threshold it lists
    And a settings tile on a phone spells its whole name

  Scenario: The transcript never widens the page
    Then a tool row named after an MCP tool wraps its label inside the row and moves its target and duration under it together
    And a long link or identifier in an answer breaks instead of widening the page

  Scenario: The on-screen keyboard opens when the reader asks for it
    Then on a touch-only device opening the start screen or a chat leaves the composer unfocused
    And a narrow desktop window still focuses the composer

  Scenario: The scroll-to-bottom button with the on-screen keyboard open
    Then the composer block and its scroll-to-bottom button rise above an overlaying keyboard
    And the scroll-to-bottom button never moves the chat up
