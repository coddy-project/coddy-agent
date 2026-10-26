Feature: Coddy drives the user's browser through Coddy for Browser
  The extension dials Coddy's browser listener and keeps one WebSocket open.
  A chat's browser actions travel down that socket as requests the extension
  answers, so Coddy reaches a browser it has no route to.

  Scenario: A chat reads a tab the user shared
    Given Coddy for Browser connected to Coddy with the extension key
    And the user shared the tab "https://example.com/" titled "Example Domain"
    When a chat lists the connected browsers
    Then the chat sees the tab "Example Domain"
    When the chat takes a snapshot of that tab
    Then the extension receives a "snapshot" command for that tab from that chat
    And the chat receives the page text "This domain is for use in examples."

  Scenario: A chat opens a tab of its own and works in it
    Given Coddy for Browser connected to Coddy with the extension key
    When a chat opens a tab on "https://example.org/"
    Then the extension receives an "open_tab" command for "https://example.org/"
    When the chat clicks the link "More information" in the tab it opened
    Then the extension receives a "click" command for the opened tab from that chat
