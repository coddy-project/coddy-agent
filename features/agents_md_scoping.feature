Feature: Nested AGENTS.md loads only for directories the agent touches
  A project can carry dozens of nested AGENTS.md files — vendored checkouts,
  monorepo packages — and loading them all costs six figures of tokens before
  the first question is answered. Nothing is walked to find them: a session
  reads an AGENTS.md the first time a filesystem tool targets its directory
  or anything below it, the whole chain of folders down to the touched path
  at once, and keeps it for the session. The root AGENTS.md is unconditional
  and always present.

  Scenario: A nested AGENTS.md enters the prompt after a tool touches its directory
    Given a project with a root AGENTS.md and nested AGENTS.md files under "internal/agent" and "external/httpserver"
    And a coddy agent session in that project
    When the model reads "internal/agent/react.go" and then answers
    Then the first request carries the root AGENTS.md but neither nested one
    And every request after the read carries the "internal/agent" AGENTS.md
    And no request carries the "external/httpserver" AGENTS.md

  Scenario: A nested AGENTS.md written after the session started is read when a tool enters its folder
    Given a project with a root AGENTS.md and a folder "internal/agent" without one
    And a coddy agent session in that project
    And a nested AGENTS.md appears under "internal/agent" after the session started
    When the model reads "internal/agent/react.go" and then answers
    Then the first request carries the root AGENTS.md but neither nested one
    And every request after the read carries the "internal/agent" AGENTS.md
