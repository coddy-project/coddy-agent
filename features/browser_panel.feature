Feature: The web UI in the side panel of Coddy for Browser
  The extension connects to the HTTP server of its Coddy and shows the web UI
  of that server in the side panel. The panel signs the web UI in with a
  one-time ticket the extension's endpoint gives it for the extension key, and
  a turn sent there works in the tab the person writes from.

  Scenario: A turn from the side panel works in the tab the user looks at
    Given a Coddy with a token-gated HTTP server, the browser enabled and a scripted model
    And Coddy for Browser connected and its side panel opened
    When the panel's web UI signs in with the ticket of the extension's endpoint
    And the user writes "read this page" in the panel from the tab "https://example.com/" titled "Example Domain"
    Then the extension receives a "snapshot" command for that tab from the panel's chat
    And the web UI receives the answer "The page is Example Domain."
    And the chat keeps the panel's browser, and the tab only for that turn
