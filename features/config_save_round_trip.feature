Feature: A settings save rewrites only what the operator changed
  The settings screen reads the configuration as JSON (GET /coddy/config) and writes
  the whole document back (PUT /coddy/config). What it reads is the configuration as
  the loader resolved it and as the process runs it: ${CODDY_HOME} and ~ already stand
  for the directories they name, ${VAR} for what the environment supplied, every field
  of a provider or a model is spelled out, and coddy serve has filled in the relay's
  listen address. A save used to write all of that back as it came, so one click on
  Save turned ${CODDY_HOME}/memory into an absolute path, gave every list entry a zero
  for each field it never named and put a start-up default into the file.

  A save now keeps what the file says for every value the operator did not change: a
  spelling that still loads as the value being saved is written back as it was, quotes
  and flow lists included, in single values and in list entries alike, and a field or a
  section the file never had stays out of it. What the operator changed is written the
  way they changed it.

  "Did not change" is measured against what that browser read. The document it reads
  names the configuration it came from (its revision), so a browser that read the
  config before another one saved leaves the other save alone in every value it did not
  touch itself.

  Scenario: A save without changes leaves config.yaml as it was
    Given a coddy server whose config.yaml spells its paths with ${CODDY_HOME}, ${CWD}, ~ and ${VAR}
    When the settings screen saves the config without changing anything
    Then config.yaml is byte for byte what it was

  Scenario: A save writes the value the operator changed and keeps the rest as written
    Given a coddy server whose config.yaml spells its paths with ${CODDY_HOME}, ${CWD}, ~ and ${VAR}
    When the settings screen saves the config with "agent.max_turns" set to 42
    Then config.yaml is what it was with "max_turns: 40" written as "max_turns: 42"

  Scenario: A save does not undo what another browser saved after this one read the config
    Given a coddy server whose config.yaml spells its paths with ${CODDY_HOME}, ${CWD}, ~ and ${VAR}
    And a second browser has read the config
    When the settings screen saves the config with "agent.max_turns" set to 42
    And the second browser saves what it read with "agent.model" set to "spare/tiny"
    Then config.yaml carries both saves and is otherwise what it was
