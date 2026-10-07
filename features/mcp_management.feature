Feature: MCP server management
  Coddy discovers MCP servers from two Cursor-compatible files: the global
  ~/.coddy/mcp.json (scope "global") and the project-local ./.coddy/mcp.json
  (scope "local"), which overrides a name the global file declares. The HTTP
  API shows each server with its tools and lets operators disable whole
  servers or individual tools; project-local switches live in the operator's
  home, without modifying the checkout. It names the environment variables and
  headers of a declaration but never returns their values: an edit sends back
  the placeholder it was shown to keep a value. Project-local entries arrive
  with the checkout, so they are probed only once approved (see
  features/mcp_project_trust.feature); entries written through this API are
  approved by the act of writing them.

  Background:
    Given a running coddy HTTP server

  Scenario: An approved project mcp.json server appears with its tools
    Given a project mcp.json defining the stdio server "demo"
    And the project MCP server "demo" is approved
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
    And the project MCP server "demo" is approved
    When I disable the tool "echo" of MCP server "demo"
    Then server "demo" exposes the tool "echo" as disabled
    And operator state records "echo" as a disabled tool of "demo"

  Scenario: Disable and re-enable a whole server
    Given a project mcp.json defining the stdio server "demo"
    And the project MCP server "demo" is approved
    When I disable the MCP server "demo"
    Then the MCP list shows server "demo" as disabled
    And operator state records server "demo" as disabled
    When I enable the MCP server "demo"
    Then the MCP list shows server "demo" from source "local" as enabled

  Scenario: Add and remove a project server over the API
    When I add a project MCP server "added" running the fake MCP command
    Then the MCP list shows server "added" from source "local" as enabled
    And the project mcp.json contains server "added"
    When I delete the MCP server "added"
    Then the MCP list does not show server "added"
    And the project mcp.json does not contain server "added"

  Scenario: The list names a server's env, never its value, and an edit keeps it
    Given a global mcp.json defining the stdio server "shared" with the env "TOKEN" set to "tok-s3cr3t-value"
    When I list the MCP servers
    Then the MCP list names the env "TOKEN" of server "shared" but not "tok-s3cr3t-value"
    When I edit the MCP server "shared" as listed, adding the env "MODE" set to "fast"
    Then the global mcp.json keeps "tok-s3cr3t-value" as the env "TOKEN" of server "shared"
    And server "shared" exposes the tool "echo" as enabled
