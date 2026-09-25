Feature: MCP server management
  Coddy discovers MCP servers from three levels: config.yaml and the global
  ~/.coddy/mcp.json (scope "global"), and the project-local ./.coddy/mcp.json
  (scope "local"), all Cursor-compatible. Later levels override earlier ones
  by name. The HTTP API shows each server with its tools and lets operators
  disable whole servers or individual tools; toggles persist into the file
  that defines the server. Project-local entries arrive with the checkout, so
  they are probed only once approved (see features/mcp_project_trust.feature);
  entries written through this API are approved by the act of writing them.

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
    And the project mcp.json records "echo" as a disabled tool of "demo"

  Scenario: Disable and re-enable a whole server
    Given a project mcp.json defining the stdio server "demo"
    And the project MCP server "demo" is approved
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

  Scenario: Adding an unpinned npx server pins the current release
    Given the npm registry reports version "1.0.14" for "@upstash/context7-mcp"
    When I add a project MCP server "context7" running "npx -y @upstash/context7-mcp"
    Then the project mcp.json runs "@upstash/context7-mcp@1.0.14" for server "context7"
    And the save response says "@upstash/context7-mcp" was pinned to "1.0.14"

  Scenario: Adding an npx server pins the release from its explicit registry
    Given the npm registry reports version "1.0.14" for "@upstash/context7-mcp"
    And a custom npm registry reports version "2.0.0" for "@upstash/context7-mcp"
    When I add a project MCP server "context7" running "npx -y --registry CUSTOM_REGISTRY @upstash/context7-mcp"
    Then the project mcp.json runs "@upstash/context7-mcp@2.0.0" for server "context7"
    And the save response says "@upstash/context7-mcp" was pinned to "2.0.0"
    And the project MCP server "context7" is approved as written

  Scenario: A pinned project entry is approved as it was written
    Given the npm registry reports version "1.0.14" for "@upstash/context7-mcp"
    When I add a project MCP server "context7" running "npx -y @upstash/context7-mcp"
    Then the project mcp.json runs "@upstash/context7-mcp@1.0.14" for server "context7"
    And the project MCP server "context7" is approved as written
