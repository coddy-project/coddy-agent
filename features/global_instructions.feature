Feature: Instructions and rules of the operator, shared by every project
  A checkout carries its own AGENTS.md and its own rule folders, and they
  apply only inside it. The standing instructions of the person running coddy
  - the style they carry from project to project, the commands they never
  want run, how they want to be answered - belong to the operator instead, so
  they live in CODDY_HOME: AGENTS.md and DESIGN.md next to config.yaml, and
  rule files under CODDY_HOME/rules. Both are read for every session whatever directory it
  starts in, they need no configuration to turn on - the file being there is
  the switch - and the project's own files join them rather than replace them,
  below the operator's.

  Background:
    Given an agent home whose "AGENTS.md" holds "USER_AGENTS_TOKEN"
    And that agent home also has a "DESIGN.md" holding "USER_DESIGN_TOKEN"
    And that agent home holds these rule files under "rules":
      | file      | frontmatter                        | body                   |
      | house.md  | description: House style           | USER_ALWAYS_RULE_TOKEN |
      | go.mdc    | globs: **/*.go; alwaysApply: false | USER_GO_RULE_TOKEN     |

  Scenario: The operator's AGENTS.md reaches a project that has none of its own
    Given a project without an AGENTS.md of its own
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "USER_AGENTS_TOKEN"

  Scenario: The operator's DESIGN.md follows their AGENTS.md, above the project's own
    Given a project whose "DESIGN.md" holds "PROJECT_DESIGN_TOKEN"
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "USER_AGENTS_TOKEN" before "USER_DESIGN_TOKEN"
    And the request carries "USER_DESIGN_TOKEN" before "PROJECT_DESIGN_TOKEN"

  Scenario: A nested folder is read for both documents the moment a tool enters it
    Given a project without an AGENTS.md of its own
    And "internal/api" holds an "AGENTS.md" with "NESTED_AGENTS_TOKEN" and a "DESIGN.md" with "NESTED_DESIGN_TOKEN"
    And a coddy agent session in that project
    When the model reads "internal/api/handler.go" and then answers
    Then the first request carries neither "NESTED_AGENTS_TOKEN" nor "NESTED_DESIGN_TOKEN"
    And every request after the read carries "NESTED_AGENTS_TOKEN" and "NESTED_DESIGN_TOKEN"

  Scenario: A project AGENTS.md joins the operator's instead of replacing it
    Given a project whose "AGENTS.md" holds "PROJECT_AGENTS_TOKEN"
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "USER_AGENTS_TOKEN" and "PROJECT_AGENTS_TOKEN"
    And the request carries "USER_AGENTS_TOKEN" before "PROJECT_AGENTS_TOKEN"

  Scenario: The project AGENTS.md is sent once, not once per prompt block
    Given a project whose "AGENTS.md" holds "PROJECT_AGENTS_TOKEN"
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "PROJECT_AGENTS_TOKEN" exactly once

  Scenario: A rule under CODDY_HOME/rules is active in a project of its own
    Given a project without an AGENTS.md of its own
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "USER_ALWAYS_RULE_TOKEN"

  Scenario: A glob-scoped rule of the operator waits for a matching file
    Given a project without an AGENTS.md of its own
    And a coddy agent session in that project
    When the model reads "internal/api/handler.go" and then answers
    Then the first request carries neither "USER_GO_RULE_TOKEN"
    And every request after the read carries "USER_GO_RULE_TOKEN"

  Scenario: The catalog names the operator's rules as their own source
    Given a project without an AGENTS.md of its own
    When the operator lists the rules catalog
    Then the catalog lists "house" from source "user" in the "claude" format
    And the catalog lists "go" from source "user" in the "cursor" format
