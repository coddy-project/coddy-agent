Feature: Project scheduler jobs run once trusted, in the shared pool
  A project job is a markdown file in <workspace>/.coddy/scheduler: it travels with the
  repository, so it is the operator's only once approved. A job the operator creates
  through Coddy is approved at once for them; the same file reaching a checkout on its
  own waits for approval, and an approved file that changes waits again. Every trusted
  job, user or project, runs in the one scheduler daemon and its one task pool, and
  ids never clash across the two scopes.

  Scenario: A project job the operator creates runs on its cron minute next to a user job
    Given a scheduler with a job "nightly" scheduled "* * * * *"
    And the operator creates a project job "lint" scheduled "* * * * *" from a session in the workspace
    Then the project job "lint" is listed as "trusted"
    When the daemon ticks at "2026-10-06T10:00:00Z" and the model answers "done"
    Then the job "nightly" has 1 run
    And the job "lint" has 1 run
    And the run of "lint" works in the workspace
    And no state file is left in the workspace

  Scenario: A project job that came with the checkout waits for approval
    Given a scheduler
    And a project job "lint" scheduled "* * * * *" committed to the workspace
    Then the project job "lint" is listed as "needs_approval"
    When the daemon ticks at "2026-10-06T10:00:00Z"
    Then the job "lint" has 0 runs
    And a manual run of "lint" is refused because the job is not trusted
    When the operator approves the project job "lint" with the digest it was shown
    Then the project job "lint" is listed as "trusted"
    When the daemon ticks at "2026-10-06T10:01:00Z" and the model answers "done"
    Then the job "lint" has 1 run

  Scenario: An approved project job that changes on disk waits for approval again
    Given a scheduler
    And a project job "lint" scheduled "* * * * *" committed to the workspace
    And the operator approves the project job "lint" with the digest it was shown
    When the project job "lint" is rewritten on disk
    Then the project job "lint" is listed as "needs_approval"
    When the daemon ticks at "2026-10-06T10:00:00Z"
    Then the job "lint" has 0 runs

  Scenario: A project job may not take the id of a user job
    Given a scheduler with a job "nightly" scheduled "0 3 * * *"
    When the operator creates a project job "nightly" scheduled "0 4 * * *" from a session in the workspace
    Then the create is refused because the id is taken
