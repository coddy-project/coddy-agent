Feature: Rules reach the model once and keep the provider's prompt cache
  A project that works with several coding agents keeps its rules for each of
  them, a deep folder describes itself with an AGENTS.md of its own, and a
  session touches files all over the tree over several turns. Whatever the
  session does, every request carries each rule at most once, carries no rule
  that nothing brought in and no copy kept for another agent, and repeats the
  request before it byte for byte up to its turn context block: the system
  message never changes, and the history only grows at its end.

  Background:
    Given a project that keeps these rules for Cursor and a copy of each for Claude Code:
      | rule      | frontmatter                 | body               |
      | wording   | alwaysApply: true           | WORDING_RULE_TOKEN |
      | go-style  | globs: **/*.go              | GO_RULE_TOKEN      |
      | api-layer | globs: internal/api/**/*.go | API_RULE_TOKEN     |
      | web-ui    | globs: web/**/*             | UI_RULE_TOKEN      |
    And its folder "internal/api" describes itself in an AGENTS.md reading "API_AGENTS_TOKEN"
    And a coddy agent session in that project

  Scenario: A session of several turns tells the model every rule it needs once and nothing else
    When the model reads "main.go", then "internal/api/handler.go", then "internal/api/routes.go", and answers
    And the user asks again, and the model reads "main.go", then "internal/api/handler.go", and answers
    Then every request carries each rule at most once
    And every request carries "WORDING_RULE_TOKEN" in its system message
    And "GO_RULE_TOKEN" reaches the model with the first read of "main.go"
    And "API_RULE_TOKEN" and "API_AGENTS_TOKEN" reach the model with the first read of "internal/api/handler.go"
    And no request carries "UI_RULE_TOKEN"
    And no request carries the copy of any rule kept for Claude Code

  Scenario: The request prefix holds for the whole session
    When the model reads "main.go", then "internal/api/handler.go", then "internal/api/routes.go", and answers
    And the user asks again, and the model reads "main.go", then "internal/api/handler.go", and answers
    Then every request opens with the system message of the first request
    And every request repeats the one before it up to its turn context block
