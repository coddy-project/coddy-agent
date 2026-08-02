Feature: Commands can run as background tasks
  run_command normally blocks until the command exits, which forces the agent to sit idle
  through long builds, watchers and batch downloads. Asking for a background run instead
  hands the command to a session-scoped task pool and returns a task id straight away, so
  the agent keeps working and collects the result later. The model states how long it
  expects the work to take; that estimate drives the status ticker, while a separate hard
  timeout is what actually terminates a runaway command.

  Scenario: A background command returns a task id instead of its output
    Given a session with an empty background task pool
    When I run a command in the background that sleeps for 5 seconds
    Then the tool reports a background task id
    And the tool does not wait for the command to finish
    And the task is running

  Scenario: The pool lists a running task with its elapsed time and estimate
    Given a session with an empty background task pool
    When I run a command in the background that sleeps for 5 seconds with an estimate of 4 seconds
    And I list the background tasks
    Then the listing names that task
    And the listing reports the task as running
    And the listing reports an estimate of 4 seconds
    And the listing reports the elapsed time

  Scenario: Output of a finished background task is retrievable
    Given a session with an empty background task pool
    When I run a command in the background that prints "background output ready"
    And I wait for that background task to finish
    Then the task succeeded
    And the task output contains "background output ready"

  Scenario: A running background task can be stopped
    Given a session with an empty background task pool
    When I run a command in the background that sleeps for 60 seconds
    And I stop that background task
    Then the task is stopped
    And the task is no longer running

  Scenario: A background task that outlives its timeout is terminated
    Given a session with an empty background task pool
    When I run a command in the background that sleeps for 60 seconds with a timeout of 1 second
    And I wait for that background task to finish
    Then the task timed out
    And the task is no longer running
