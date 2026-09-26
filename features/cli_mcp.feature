Feature: /mcp in the console
  /mcp lists the MCP servers of the console's workspace: the global ones of
  config.yaml and <home>/mcp.json and the project ones of .coddy/mcp.json, each
  with its scope, its status and how many tools it listed. Enter opens a
  server's controls. A switch reaches the live session at once: a server
  switched off is disconnected, so its tools leave the next turn, while the
  other servers keep running. A project declaration arrives with the checkout,
  so it starts only once the operator approved it, after the console showed
  what it would run.

  Scenario: Switching a server off disconnects it from the live session
    Given a coddy console app over a stub agent runner with the global MCP server "toggle-mcp"
    When the console app starts
    And the operator submits the command "/mcp"
    Then the MCP list shows "toggle-mcp" as "global · connected · 1 tools"
    And the session is connected to the MCP server "toggle-mcp"
    When the operator opens the highlighted MCP server
    And the operator chooses "Toggle server"
    Then the MCP list shows "toggle-mcp" as "global · disabled · 0 tools"
    And the session is not connected to the MCP server "toggle-mcp"

  Scenario: A project server starts once the declaration shown is approved
    Given a coddy console app over a stub agent runner with the project MCP server "proj-mcp"
    When the console app starts
    And the operator submits the command "/mcp"
    Then the MCP list shows "proj-mcp" as "local · needs_approval · 0 tools"
    And the session is not connected to the MCP server "proj-mcp"
    When the operator opens the highlighted MCP server
    And the operator chooses "Grant trust"
    And the operator highlights "Approve this declaration"
    Then the confirmation shows what "proj-mcp" would run
    When the operator chooses "Approve this declaration"
    Then the MCP list shows "proj-mcp" as "local · connected · 1 tools"
    And the session is connected to the MCP server "proj-mcp"
