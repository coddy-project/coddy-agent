Feature: coddy serve runs the subsystems the config enables
  As an operator I run one process and turn surfaces on and off in config.yaml,
  so a swarm node can serve the HTTP API, poll Telegram and run cron jobs at once
  instead of one process per surface.

  Scenario: the HTTP API is the default surface
    Given a config with no httpserver section
    When the runtime resolves which subsystems to start
    Then the "httpserver" subsystem is enabled
    And the "scheduler" subsystem is disabled
    And the "gateway" subsystem is disabled
    And the "swarm" subsystem is disabled

  Scenario: the HTTP API binds loopback until the operator says otherwise
    Given a config with no httpserver section
    When the runtime resolves the httpserver listen address
    Then the listen address is "127.0.0.1:12345"

  Scenario: enabling a surface in config is all it takes
    Given a config enabling the telegram gateway and the scheduler
    When the runtime resolves which subsystems to start
    Then the "httpserver" subsystem is enabled
    And the "gateway" subsystem is enabled
    And the "scheduler" subsystem is enabled

  Scenario: an API-only node keeps the web surface off
    Given a config with httpserver disabled and the telegram gateway enabled
    When the runtime resolves which subsystems to start
    Then the "httpserver" subsystem is disabled
    And the "gateway" subsystem is enabled

  Scenario: a subsystem the binary cannot run is a startup error
    Given a config enabling the telegram gateway
    And a binary built without gateway support
    When the runtime resolves which subsystems to start
    Then resolving fails naming the build tag "gateway"

  Scenario: a process with nothing enabled refuses to start
    Given a config with every subsystem disabled
    When the runtime resolves which subsystems to start
    Then resolving fails asking for a subsystem to be enabled

  Scenario: rotating the bot token restarts the gateway alone
    Given a running runtime with the httpserver and the telegram gateway enabled
    When the telegram token is changed through the configuration
    Then the "gateway" subsystem is restarted
    And the "httpserver" subsystem keeps running

  Scenario: turning a surface on through the configuration starts it
    Given a running runtime with the httpserver and the telegram gateway enabled
    When the scheduler is enabled through the configuration
    Then the "scheduler" subsystem is running
    And the "httpserver" subsystem keeps running

  Scenario: turning a surface off through the configuration stops it
    Given a running runtime with the httpserver and the telegram gateway enabled
    When the telegram gateway is disabled through the configuration
    Then the "gateway" subsystem is stopped
    And the "httpserver" subsystem keeps running
