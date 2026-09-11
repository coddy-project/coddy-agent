Feature: coddy serve --dry-run checks the addresses it would bind
  A daemon that fails to bind its port dies in a log file nobody is tailing yet.
  coddy serve --dry-run resolves the subsystems the configuration enables exactly as
  a start would, flags included, and tries each listen address once, so a taken port
  is reported in the terminal that typed the command, next to the line that set it.

  Scenario: the HTTP listen address is checked before the daemon starts
    Given a config.yaml with the HTTP API on a free port and a provider that answers
    When I run coddy serve with --dry-run
    Then the command succeeds
    And the report marks httpserver as ok mentioning "free"

  Scenario: a port another process holds is reported at httpserver.port
    Given a config.yaml with the HTTP API on a port another process holds
    When I run coddy serve with --dry-run
    Then the command fails
    And the report marks httpserver as an error mentioning "in use"
    And the report points at the line of "port:"
