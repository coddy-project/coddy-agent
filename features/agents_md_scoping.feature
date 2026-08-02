Feature: Nested AGENTS.md loads only for directories the agent touches
  A project can carry dozens of nested AGENTS.md files — vendored checkouts,
  monorepo packages — and loading them all costs six figures of tokens before
  the first question is answered. Each nested file is scoped to its own
  directory: it enters the system prompt the first time a filesystem tool
  targets that directory or anything below it, then stays for the session.
  The root AGENTS.md is unconditional and always present.

  Scenario: A nested AGENTS.md enters the prompt after a tool touches its directory
    Given a project with a root AGENTS.md and nested AGENTS.md files under "internal/agent" and "external/httpserver"
    And a coddy agent session in that project
    When the model reads "internal/agent/react.go" and then answers
    Then the first request carries the root AGENTS.md but neither nested one
    And every request after the read carries the "internal/agent" AGENTS.md
    And no request carries the "external/httpserver" AGENTS.md
