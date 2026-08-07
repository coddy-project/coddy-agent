Feature: Agent-managed Coddy configuration
  Coddy exposes its own YAML configuration to the agent through two typed tools,
  so a request such as "find a browser MCP and install it" can be completed
  without hand-editing config.yaml or restarting the process.

  "config_get" reads one slash-delimited path and redacts secret-shaped values.
  The permission-gated "config_set" edits exactly that one path, validates the
  resulting typed config, writes it atomically with a backup, and hot-reloads the
  running session so the newly configured capability is usable in the same turn.

  Paths are JSON Pointer-like with an XPath-style selector for named entries:
  "/agent/max_turns" walks mappings, "/skills/dirs/-" appends to a sequence, and
  "/mcp_servers[name=context7]" selects a sequence entry by a scalar field, or
  appends it when no entry matches.

  Background:
    Given an active Coddy config:
      """
      agent:
        max_turns: 17
      skills:
        dirs:
          - /opt/coddy/skills
      mcp_servers:
        - name: filesystem
          command: npx
          args: ["-y", "@modelcontextprotocol/server-filesystem"]
          env:
            - name: ROOT_TOKEN
              value: super-secret
      """
    And the session can hot-reload its runtime configuration

  Scenario: Read one setting instead of the whole file
    When the agent reads config path "/mcp_servers[name=filesystem]/command"
    Then the read returns "npx"
    And the read names the active config file

  Scenario: Credentials are redacted on read
    When the agent reads config path "/mcp_servers[name=filesystem]"
    Then the read is marked as redacted
    And the read does not expose "super-secret"

  Scenario: Install an MCP server and reload the runtime
    When the agent sets config path "/mcp_servers[name=context7]" to:
      """
      {"name":"context7","command":"npx","args":["-y","@upstash/context7-mcp"]}
      """
    Then the config update succeeds and reports the file was changed
    And the runtime config is reloaded once
    And config path "/mcp_servers[name=context7]/command" equals "npx"
    And config path "/mcp_servers[name=filesystem]/command" equals "npx"
    And the reloaded config still limits the agent to 17 turns
    And a config backup sits next to the active file

  Scenario: Change one field of an already configured MCP server
    When the agent sets config path "/mcp_servers[name=filesystem]/disabled" to:
      """
      true
      """
    Then the config update succeeds and reports the file was changed
    And the runtime config is reloaded once
    And config path "/mcp_servers[name=filesystem]/disabled" equals "true"
    And config path "/mcp_servers[name=filesystem]/command" equals "npx"

  Scenario: Register another skills directory by appending to a sequence
    When the agent sets config path "/skills/dirs/-" to:
      """
      "/home/dev/.agents/skills"
      """
    Then the config update succeeds and reports the file was changed
    And the runtime config is reloaded once
    And config path "/skills/dirs/0" equals "/opt/coddy/skills"
    And config path "/skills/dirs/1" equals "/home/dev/.agents/skills"

  Scenario: Remove a configured MCP server
    When the agent deletes config path "/mcp_servers[name=filesystem]"
    Then the config update succeeds and reports the file was changed
    And the runtime config is reloaded once
    And config path "/mcp_servers[name=filesystem]" is absent
    And the reloaded config still limits the agent to 17 turns
