Feature: The web UI on a phone
  A phone gives the web UI 360 to 430 CSS pixels and a keyboard without
  Shift+Enter. What Enter does in the composer follows the input device, not
  the width of the window: wherever a keyboard is attached, a narrow desktop
  window included, Enter sends and Shift+Enter or Ctrl+Enter starts a new line;
  on a touch-only phone Return stays a newline and the Send button sends. The
  layout gives a phone one-line chip strips that scroll sideways beside the
  controls that must stay put, a top bar whose icons never cover the brand
  and fold behind a More button when they do not fit, a start screen that
  never widens the page, and text fields large enough that iOS Safari does
  not zoom into them. Tablets and desktops keep their layout.

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
