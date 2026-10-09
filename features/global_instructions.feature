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

  The AGENTS.md and DESIGN.md documents come in three layers, always and in
  this order: the agent home's pair, the session folder's pair, and the pair
  of every folder on the way down to a file the agent works with, which
  arrives with that tool result. No rules setting and no prompt template turns
  a layer off. instructions.files is empty by default and only adds files
  below the layers, and every file reaches the model once, however many ways
  lead to it.

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

  Scenario: Without any setting a request carries the three layers in their order
    Given a project whose "AGENTS.md" holds "PROJECT_AGENTS_TOKEN"
    And "internal/api" holds an "AGENTS.md" with "NESTED_AGENTS_TOKEN" and a "DESIGN.md" with "NESTED_DESIGN_TOKEN"
    And a coddy agent session in that project
    When the model reads "internal/api/handler.go" and then answers
    Then the request carries "USER_AGENTS_TOKEN" before "USER_DESIGN_TOKEN"
    And the request carries "USER_DESIGN_TOKEN" before "PROJECT_AGENTS_TOKEN"
    And the first request carries neither "NESTED_AGENTS_TOKEN" nor "NESTED_DESIGN_TOKEN"
    And every request after the read carries "NESTED_AGENTS_TOKEN" and "NESTED_DESIGN_TOKEN"

  Scenario: A prompts.dir template that prints neither block still gets every layer
    Given a project whose "AGENTS.md" holds "PROJECT_AGENTS_TOKEN"
    And "internal/api" holds an "AGENTS.md" with "NESTED_AGENTS_TOKEN" and a "DESIGN.md" with "NESTED_DESIGN_TOKEN"
    And the operator's prompts.dir template prints neither {{.Rules}} nor {{.Instructions}}
    And a coddy agent session in that project
    When the model reads "internal/api/handler.go" and then answers
    Then the request carries "USER_AGENTS_TOKEN" before "PROJECT_AGENTS_TOKEN"
    And every request after the read carries "NESTED_AGENTS_TOKEN" and "NESTED_DESIGN_TOKEN"

  Scenario Outline: <setting> does not turn the nested documents off
    Given a project without an AGENTS.md of its own
    And "internal/api" holds an "AGENTS.md" with "NESTED_AGENTS_TOKEN" and a "DESIGN.md" with "NESTED_DESIGN_TOKEN"
    And the configuration sets <setting>
    And a coddy agent session in that project
    When the model reads "internal/api/handler.go" and then answers
    Then every request after the read carries "NESTED_AGENTS_TOKEN" and "NESTED_DESIGN_TOKEN"

    Examples:
      | setting                    |
      | rules.auto_discover: false |
      | rules.systems: [coddy]     |

  Scenario: Files of instructions.files come after the layers, in the order listed
    Given a project whose "AGENTS.md" holds "PROJECT_AGENTS_TOKEN"
    And the project also has "TEAM.md" holding "TEAM_TOKEN" and "docs/STYLE.md" holding "STYLE_TOKEN"
    And instructions.files lists "docs/STYLE.md" and "TEAM.md"
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "PROJECT_AGENTS_TOKEN" before "STYLE_TOKEN"
    And the request carries "STYLE_TOKEN" before "TEAM_TOKEN"

  Scenario: A session opened in the agent home itself reads the operator's pair once
    Given a coddy agent session in the agent home itself
    When the model answers without touching any file
    Then the request carries "USER_AGENTS_TOKEN" exactly once
    And the request carries "USER_DESIGN_TOKEN" exactly once

  Scenario: A project AGENTS.md that links to the operator's is sent once
    Given a project whose AGENTS.md is a link to the agent home's
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "USER_AGENTS_TOKEN" exactly once

  Scenario: instructions.files naming the project's own pair adds nothing
    Given a project whose "AGENTS.md" holds "PROJECT_AGENTS_TOKEN"
    And instructions.files lists "AGENTS.md" and "DESIGN.md"
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "PROJECT_AGENTS_TOKEN" exactly once

  Scenario: A nested AGENTS.md that instructions.files names is not attached again
    Given a project without an AGENTS.md of its own
    And "docs" holds an "AGENTS.md" with "DOCS_AGENTS_TOKEN" and a "DESIGN.md" with "DOCS_DESIGN_TOKEN"
    And instructions.files lists "docs/AGENTS.md"
    And a coddy agent session in that project
    When the model reads "docs/guide.md" and then answers
    Then every request carries "DOCS_AGENTS_TOKEN" exactly once
    And every request after the read carries "DOCS_DESIGN_TOKEN"

  Scenario: A file shared by several agents is read by its absolute path, named and counted as rules
    Given a folder outside the agent home and the project holds "house-style.md" with "SHARED_STYLE_TOKEN"
    And instructions.files lists that shared file by its absolute path
    And a project without an AGENTS.md of its own
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "SHARED_STYLE_TOKEN"
    And the request names the shared file in a heading above its text
    And the context estimate counts the shared file under rules

  Scenario: An absolute entry of instructions.files that cannot be read is reported
    Given instructions.files lists an absolute path that does not exist
    And a project without an AGENTS.md of its own
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the log warns that the instructions file was not read and names its path
