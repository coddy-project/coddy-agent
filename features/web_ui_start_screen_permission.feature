Feature: The start screen's permission mode in the web UI
  Before a chat has a session there is no settings snapshot to read, and the
  start screen used to show "Ask first" whatever the configuration said; a
  mode picked there never reached the first turn. The chip now names the mode
  the server is configured with, and a pick rides in with the first message.

  Scenario: The start screen shows the configured mode and keeps a pick
    Then the start screen's chip names the permission mode the server is configured with
    And an explicit Ask first under a bypass configuration rides in with the first message
    And a pick of another mode rides in with the first message
