Feature: The HTTP surface exposes background tasks of a session
  A background task outlives the turn that started it, so the SSE stream of that turn cannot
  be what keeps the interface honest. The tasks drawer polls a REST surface instead: one
  endpoint lists what the session is running with its status and timing, one returns a
  single task together with the output captured so far, and one stops a task. Tasks left
  behind by an earlier coddy process are still listed, reported as orphaned rather than as
  running forever.

  Scenario: The task list reports what the session is running
    Given a running coddy http server with a session
    And that session started a long background command
    When I GET the background tasks of that session
    Then the response lists that task as running
    And the response reports one running task
    And the listed task carries its elapsed time and its estimate

  Scenario: A single task returns the output captured so far
    Given a running coddy http server with a session
    And that session started a background command printing "http background ready"
    When I GET that background task
    Then the task response contains "http background ready"

  Scenario: Stopping a task through the API terminates it
    Given a running coddy http server with a session
    And that session started a long background command
    When I POST a stop for that background task
    Then the task response reports the task as stopped
    And a later listing no longer reports a running task

  Scenario: A task left behind by an earlier process is listed as orphaned
    Given a running coddy http server with a session
    And the session bundle records a background task that was still running
    When I GET the background tasks of that session
    Then the response lists that task as orphaned

  Scenario: An unknown task id is reported as missing
    Given a running coddy http server with a session
    When I GET a background task that does not exist
    Then the API answers 404

  Scenario: A malformed tail is rejected rather than silently ignored
    Given a running coddy http server with a session
    And that session started a long background command
    When I GET that background task with tail "abc"
    Then the API answers 400
