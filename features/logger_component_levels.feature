Feature: Log verbosity is raised one subsystem at a time
  A single logger.level is all-or-nothing. Chasing a Telegram command that does
  not work means turning the whole process to debug, and every other subsystem
  drowns the lines that matter in a file an operator reads with tail.

  logger.levels names components and gives each its own minimum severity. A
  component is a dotted path, so "gateway" covers "gateway.telegram" without
  naming it, and the longest configured prefix wins. Records that carry no
  component keep following logger.level.

  The same spec is accepted by --log-level as a comma-separated list, so an
  operator running under systemd can raise one subsystem for a single restart
  without editing the configuration file.

  Background:
    Given a logger whose root level is "info"

  Scenario: A component override raises verbosity above the root level
    Given the component "gateway.telegram" is configured at "debug"
    When the component "gateway.telegram" logs a debug record "callback received"
    And the component "session" logs a debug record "session loaded"
    Then the log contains "callback received"
    And the log does not contain "session loaded"

  Scenario: A component override lowers verbosity below the root level
    Given the component "session" is configured at "error"
    When the component "session" logs an info record "session loaded"
    And the component "gateway.telegram" logs an info record "bot connected"
    Then the log contains "bot connected"
    And the log does not contain "session loaded"

  Scenario: A parent component covers the components nested under it
    Given the component "gateway" is configured at "debug"
    When the component "gateway.telegram" logs a debug record "prompt turn"
    Then the log contains "prompt turn"

  Scenario: The longest configured prefix decides the level
    Given the component "gateway" is configured at "debug"
    And the component "gateway.telegram" is configured at "warn"
    When the component "gateway.telegram" logs a debug record "prompt turn"
    And the component "gateway.hub" logs a debug record "adapter starting"
    Then the log contains "adapter starting"
    And the log does not contain "prompt turn"

  Scenario: Records without a component follow the root level
    When an untagged debug record "raw line" is logged
    And an untagged info record "startup" is logged
    Then the log contains "startup"
    And the log does not contain "raw line"

  Scenario: The command line raises one component without touching the config
    When the process is started with --log-level "info,gateway.telegram=debug"
    And the component "gateway.telegram" logs a debug record "callback received"
    And the component "session" logs a debug record "session loaded"
    Then the log contains "callback received"
    And the log does not contain "session loaded"
