Feature: Checking config.yaml before anything starts
  An operator edits ~/.coddy/config.yaml by hand and learns about a typo only when a
  surface misbehaves, because the loader decodes the file leniently: a key it does not
  know is ignored, and a value of the wrong shape surfaces as a runtime error somewhere
  else. The -t / --test-config flag of the console, of coddy acp and of coddy serve checks
  the file the process would load against the published JSON Schema and the loader's own
  rules, then prints every problem with its line, what is wrong and how to fix it. It starts
  nothing and touches nothing: the backup recovery a normal load performs never runs.

  Scenario: a valid config passes and nothing is started or created
    Given a config.yaml with one provider and one model
    When I run coddy serve with --test-config
    Then the command succeeds
    And the report says the config is valid
    And nothing was created under the home besides config.yaml

  Scenario: a misspelled key is reported with its line and the key that was meant
    Given a config.yaml whose httpserver section says "enabled: true" on line 3
    When I run coddy with -t
    Then the command fails
    And the report points at line 3 of the config file
    And the report names the unknown key "enabled" and suggests "enable"

  Scenario: a value outside the allowed set comes with the values that are allowed
    Given a config.yaml whose logger.level is "verbose"
    When I run coddy serve with --test-config
    Then the command fails
    And the report points at the line of "verbose"
    And the report lists the allowed values "debug, info, warn, warning, error"

  Scenario: the check never rewrites the file
    Given a config.yaml with a broken value and a valid config.yaml.bak beside it
    When I run coddy acp with -t
    Then the command fails
    And config.yaml still has the broken value
