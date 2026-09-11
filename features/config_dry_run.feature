Feature: A dry run checks that the configured world exists before anything starts
  A config.yaml can be valid to the letter and still describe a world that is not
  there: a model server that is down, a key the provider rejects, an MCP command that
  is not installed, a revoked Telegram token, a prompts directory that was renamed. Today
  each of those surfaces as a runtime error in whichever surface hits it first. --dry-run
  on the console, on coddy acp and on coddy serve checks the file, then probes what it
  points at - paths, servers, credentials - and reports every problem with the place in
  config.yaml it comes from. On its own it is quiet: problems and one status line. With
  --test-config alongside it shows the config check report and every probe, the ones
  that passed included. Nothing starts and nothing is written.

  Scenario: a healthy setup answers with one status line
    Given a config.yaml whose provider "local" points at a model server listing "qwen"
    And the config uses model "local/qwen"
    When I run coddy with --dry-run
    Then the command succeeds
    And the report is a single status line starting with "dry run: 0 errors, 0 warnings"

  Scenario: with --test-config the passed probes are shown too
    Given a config.yaml whose provider "local" points at a model server listing "qwen"
    And the config uses model "local/qwen"
    When I run coddy with --dry-run --test-config
    Then the command succeeds
    And the report says the config is valid
    And the report marks providers[local] as ok mentioning "1 model"
    And the report marks models[local/qwen] as ok

  Scenario: a rejected credential is reported at the provider's line
    Given a config.yaml whose provider "hosted" points at a model server that rejects every request
    When I run coddy acp with --dry-run
    Then the command fails
    And the report marks providers[hosted] as an error mentioning "HTTP 401"
    And the report points at the line of "name: hosted"

  Scenario: an MCP command that is not installed is reported
    Given a config.yaml with an MCP server "tools" whose command is "definitely-not-installed-coddy-mcp"
    When I run coddy with --dry-run
    Then the command fails
    And the report marks mcp_servers[tools] as an error mentioning "not found"
    And the report points at the line of "command:"

  Scenario: a Telegram token is checked against the Bot API
    Given a config.yaml enabling the Telegram gateway with a token the Bot API accepts as "coddy_dry_run_bot"
    When I run coddy acp with --dry-run --test-config
    Then the command succeeds
    And the report marks gateways.telegram as ok mentioning "@coddy_dry_run_bot"

  Scenario: a prompts directory that does not exist is an error at its line
    Given a config.yaml whose prompts.dir points at a folder that does not exist
    When I run coddy with --dry-run
    Then the command fails
    And the report marks prompts.dir as an error mentioning "does not exist"
    And the report points at the line of "dir:"

  Scenario: a broken file stops the dry run at the static check
    Given a config.yaml whose httpserver section says "enabled: true" on line 3
    When I run coddy with --dry-run
    Then the command fails
    And the report points at line 3 of the config file
