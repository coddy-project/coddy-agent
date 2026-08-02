Feature: Agent-managed Coddy configuration
  Coddy can update one setting in its active config file and reload the
  running session without replacing unrelated configuration.

  Scenario: Add an MCP server and reload the runtime
    Given an active Coddy config with no MCP servers
    When the agent sets config path "/mcp_servers[name=context7]" to:
      """
      {"name":"context7","command":"npx","args":["-y","@upstash/context7-mcp"]}
      """
    Then the config update succeeds
    And the runtime config is reloaded once
    And config path "/mcp_servers[name=context7]/command" equals "npx"
    And the config still contains the unrelated agent setting
