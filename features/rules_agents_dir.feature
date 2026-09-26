Feature: Rules from the shared .agents/rules folder
  The .agents/ directory is the tool-neutral home for agent configuration:
  skills already live in .agents/skills. A project can keep its rules in
  .agents/rules and coddy picks them up out of the box, ahead of the
  tool-specific folders of other agents. The file extension names the dialect:
  a .mdc file is a Cursor rule (description, globs, alwaysApply), a .md file is
  a Claude Code rule (paths; loaded unconditionally when paths is absent).

  Background:
    Given a project whose ".agents/rules" folder holds these rule files:
      | file          | frontmatter                        | body               |
      | always.mdc    | alwaysApply: true                  | ALWAYS_RULE_TOKEN  |
      | style.md      | description: House style           | STYLE_RULE_TOKEN   |
      | runbook.mdc   | description: Deploy runbook        | RUNBOOK_RULE_TOKEN |
      | go.mdc        | globs: **/*.go; alwaysApply: false | GO_RULE_TOKEN      |
      | http-layer.md | paths: ["internal/api/**/*.go"]    | HTTP_RULE_TOKEN    |

  Scenario: The catalog lists .agents/rules files with the dialect taken from the extension
    When the operator lists the rules catalog
    Then the catalog lists "go" from source "agents-dir" in the "cursor" format
    And the catalog lists "http-layer" from source "agents-dir" in the "claude" format

  Scenario: The extension decides whether a description-only rule is always on
    Given a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "ALWAYS_RULE_TOKEN" and "STYLE_RULE_TOKEN"
    And the request carries neither "RUNBOOK_RULE_TOKEN", "GO_RULE_TOKEN" nor "HTTP_RULE_TOKEN"

  Scenario: Path-scoped rules of both dialects arrive with the read of a matching file
    Given a coddy agent session in that project
    When the model reads "internal/api/handler.go" and then answers
    Then the first request carries neither "GO_RULE_TOKEN" nor "HTTP_RULE_TOKEN"
    And the result of the read carries "GO_RULE_TOKEN" and "HTTP_RULE_TOKEN"
    And every request opens with the same system message

  Scenario: A mention-only Cursor rule rides in the user's message when the user names it
    Given a coddy agent session in that project
    When the user asks "deploy it, follow @runbook" and the model answers
    Then the user's message carries "RUNBOOK_RULE_TOKEN"
    And the system prompt carries neither "RUNBOOK_RULE_TOKEN"
