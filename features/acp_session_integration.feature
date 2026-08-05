Feature: ACP session integrations
  ACP clients receive schema-compatible session updates, and live settings
  changes affect the session that is already open.

  Scenario: Loaded skills are advertised after session creation
    Given Coddy ACP has loaded skill "context-router"
    When an ACP client creates a session
    Then the session/new response is sent before its session updates
    And the available slash commands include "context-router"

  Scenario: A reopened persisted session replays its transcript after the response
    Given Coddy ACP reopens a persisted session holding an earlier exchange
    When an ACP client creates a session
    Then the session/new response is sent before its session updates
    And the replayed transcript includes "previous answer"

  Scenario: Current mode updates use the ACP wire schema
    Given an ACP client has an active session
    When the client switches the mode to "plan"
    Then the current mode update identifies "plan" with "currentModeId"
    And the current mode update does not contain "modeId"

  Scenario: A configured MCP server is connected to the active session
    Given an active session without configured MCP servers
    When settings are reloaded with MCP server "settings-probe"
    Then the current session exposes MCP tool "settings-probe__probe"
