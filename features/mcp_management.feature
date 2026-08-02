Feature: MCP server management
  Coddy discovers MCP servers from three levels: config.yaml and the global
  ~/.coddy/mcp.json (scope "global"), and the project-local ./.coddy/mcp.json
  (scope "local"), all Cursor-compatible. Later levels override earlier ones
  by name. The HTTP API shows each server with its tools and lets operators
  disable whole servers or individual tools; toggles persist into the file
  that defines the server.

  Background:
    Given a running coddy HTTP server

  Scenario: A project mcp.json server appears with its tools
    Given a project mcp.json defining the stdio server "demo"
    When I list the MCP servers
    Then the MCP list shows server "demo" from source "local" as enabled
    And server "demo" exposes the tool "echo" as enabled

  Scenario: A global mcp.json server appears with global scope
    Given a global mcp.json defining the stdio server "shared"
    When I list the MCP servers
    Then the MCP list shows server "shared" from source "global" as enabled
    And server "shared" exposes the tool "echo" as enabled

  Scenario: Disable a single tool of a server
    Given a project mcp.json defining the stdio server "demo"
    When I disable the tool "echo" of MCP server "demo"
    Then server "demo" exposes the tool "echo" as disabled
    And the project mcp.json records "echo" as a disabled tool of "demo"

  Scenario: Disable and re-enable a whole server
    Given a project mcp.json defining the stdio server "demo"
    When I disable the MCP server "demo"
    Then the MCP list shows server "demo" as disabled
    And the project mcp.json records server "demo" as disabled
    When I enable the MCP server "demo"
    Then the MCP list shows server "demo" from source "local" as enabled

  Scenario: Add and remove a project server over the API
    When I add a project MCP server "added" running the fake MCP command
    Then the MCP list shows server "added" from source "local" as enabled
    And the project mcp.json contains server "added"
    When I delete the MCP server "added"
    Then the MCP list does not show server "added"
    And the project mcp.json does not contain server "added"
