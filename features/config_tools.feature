Feature: Agent-managed Coddy configuration
  Coddy exposes its own YAML configuration to the agent through UCI-style typed
  tools, so a request such as "find a browser MCP and install it" can be
  completed without hand-editing config.yaml or restarting the process.

  "config_get" reads one dotted path and redacts secret-shaped values. Edits are
  staged like OpenWrt's uci CLI: "config_set" records commands (set, add_list,
  del_list, delete) without touching the active file, "config_changes" lists the
  pending commands, and nothing applies until the user agrees and the
  permission-gated "config_commit" validates the batch, snapshots the previous
  file next to the config, writes atomically, and hot-reloads the running
  session. "config_revert" discards pending commands, and "config_rollback"
  restores the pre-commit snapshot with a warning.

  Paths are dotted like uci with a selector for named sequence entries:
  "agent.max_turns" walks mappings, "skills.dirs.0" indexes a sequence, and
  "mcp_servers[name=context7]" selects a sequence entry by a scalar field, or
  appends it on set when no entry matches.

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
    When the agent reads config path "mcp_servers[name=filesystem].command"
    Then the read returns "npx"
    And the read names the active config file

  Scenario: Credentials are redacted on read
    When the agent reads config path "mcp_servers[name=filesystem]"
    Then the read is marked as redacted
    And the read does not expose "super-secret"

  Scenario: Staged commands do not touch the active config
    When the agent stages config commands:
      """
      set agent.max_turns=20
      add_list skills.dirs=/home/dev/.agents/skills
      """
    Then the staging result lists 2 pending commands
    And config path "agent.max_turns" equals "17"
    And the runtime config is not reloaded

  Scenario: Review pending commands before asking the user to save
    When the agent stages config commands:
      """
      set agent.max_turns=20
      """
    And the agent lists config changes
    Then the change list shows "set agent.max_turns=20"

  Scenario: Install an MCP server and reload on commit
    When the agent stages config commands:
      """
      set mcp_servers[name=context7]={"name":"context7","command":"npx","args":["-y","@upstash/context7-mcp"]}
      """
    And the agent commits the staged config
    Then the commit succeeds and reports the applied commands
    And the runtime config is reloaded once
    And config path "mcp_servers[name=context7].command" equals "npx"
    And config path "mcp_servers[name=filesystem].command" equals "npx"
    And the reloaded config still limits the agent to 17 turns
    And a pre-commit snapshot sits next to the active file
    And no config commands remain staged

  Scenario: Register another skills directory by appending to a list
    When the agent stages config commands:
      """
      add_list skills.dirs=/home/dev/.agents/skills
      """
    And the agent commits the staged config
    Then the commit succeeds and reports the applied commands
    And config path "skills.dirs.0" equals "/opt/coddy/skills"
    And config path "skills.dirs.1" equals "/home/dev/.agents/skills"

  Scenario: Revert discards pending commands without applying them
    When the agent stages config commands:
      """
      set agent.max_turns=20
      """
    And the agent reverts the staged config
    Then no config commands remain staged
    And config path "agent.max_turns" equals "17"
    And the runtime config is not reloaded

  Scenario: Remove a configured MCP server through the staged flow
    When the agent stages config commands:
      """
      delete mcp_servers[name=filesystem]
      """
    And the agent commits the staged config
    Then the commit succeeds and reports the applied commands
    And config path "mcp_servers[name=filesystem]" is absent
    And the reloaded config still limits the agent to 17 turns

  Scenario: Roll back to the previous configuration from the snapshot
    When the agent stages config commands:
      """
      set agent.max_turns=20
      """
    And the agent commits the staged config
    And the agent rolls back the committed config
    Then the rollback warns that the previous configuration replaced the current one
    And config path "agent.max_turns" equals "17"
    And the runtime config is reloaded twice
