Feature: A saved config keeps its comments and names the editor schema
  Operators edit ~/.coddy/config.yaml by hand as often as through the settings screen,
  so the file carries their comments and a "# yaml-language-server:" header pointing a
  YAML language server at Coddy's published JSON Schema. Rewriting the file from the
  settings screen used to serialize the config structs from scratch, which dropped every
  comment - the schema header included, so the editor stopped validating the file right
  after the first save. A save now merges into the file already on disk: comments and key
  order survive, and a config with no header gets the published one.

  Scenario: A settings save keeps the comments the operator wrote
    Given a coddy gateway whose config.yaml carries operator comments
    When the settings screen saves the config with "agent.max_turns" set to 42
    Then the saved config.yaml still carries the operator comments
    And the saved config.yaml sets "agent.max_turns" to 42

  Scenario: A saved config points editors at the published schema
    Given a coddy gateway whose config.yaml has no schema header
    When the settings screen saves the config with "agent.max_turns" set to 12
    Then the saved config.yaml starts with the schema header for "https://coddy.dev/config.schema.json"

  Scenario: A schema header the operator chose is left alone
    Given a coddy gateway whose config.yaml points its editor at "./config.schema.json"
    When the settings screen saves the config with "agent.max_turns" set to 12
    Then the saved config.yaml still points its editor at "./config.schema.json"
    And the saved config.yaml carries exactly one schema header
