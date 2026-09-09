Feature: A configuration reload reaches the clients that are already open
  Coddy hot-reloads its own configuration: the settings form saves it, the agent's
  config_commit and config_rollback tools apply it mid-turn, installing a skill
  rewrites it. Every one of those swaps changes what the server answers - a model
  added to models[] is in GET /v1/models the moment the swap lands.

  A client that is already open learned that list once, at boot, so without a signal
  it keeps offering the previous one until someone reloads the page. GET /coddy/events
  carries the signal: every swap of the live configuration publishes one
  config_reloaded event, and a client re-reads whichever config-derived list it
  renders. The event names nothing itself - what changed is already behind
  GET /v1/models and GET /coddy/slash-commands, and a payload carrying it would only
  be another copy to keep in step.

  Background:
    Given a coddy server with a saved configuration
    And a browser is subscribed to the server event stream

  Scenario: A model added to the configuration reaches an open model picker
    When the configuration is saved with the model "rpa/qwen3.6-35b-a3b" added
    Then the browser is told the configuration reloaded
    And the model list the browser reads after that event carries "rpa/qwen3.6-35b-a3b"

  Scenario: Every open client hears the same reload
    Given a second browser is subscribed to the server event stream
    When the configuration is saved with the model "rpa/qwen3.6-35b-a3b" added
    Then both browsers are told the configuration reloaded
