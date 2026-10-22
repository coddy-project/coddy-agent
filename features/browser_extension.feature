Feature: A browser extension connected to Coddy
  Scenario: An agent reads a tab selected in the extension
    Given a browser extension connected to Coddy
    And the user shared an example tab
    When a chat asks for the page snapshot
    Then the extension receives the command for the shared tab
    And the chat receives the page contents
