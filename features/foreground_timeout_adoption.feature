Feature: A foreground command that outlives its timeout is handed over, not lost
  run_command waits thirty seconds by default and then has to decide what to do with a command
  that is still working. Killing the shell is the wrong answer twice over: a dev server or a
  watcher is doing exactly what was asked, and anything the shell spawned survives a kill aimed
  only at the shell while still holding the output pipe, so the tool call itself never returns.
  The command is handed to the session task pool instead, keeping its process group and its
  output stream intact, and the tool answers with the new task id followed by everything captured
  so far - so the agent learns the command works, and never re-runs it with a bigger timeout.

  Scenario: A command still running at its timeout becomes a background task
    Given a session with an empty background task pool
    When I run a command in the foreground that keeps running past its 2 second timeout
    Then the tool answers within 20 seconds
    And the answer names a new background task
    And that background task is running

  Scenario: The handover answer carries the output produced before the timeout
    Given a session with an empty background task pool
    When I run a command in the foreground that prints "serving on port 8081" and keeps running past its 2 second timeout
    Then the answer contains "serving on port 8081"
    And the answer names the background task before it shows that output

  Scenario: The handover answer carries what the command wrote to its error stream
    Given a session with an empty background task pool
    When I run a command in the foreground that complains "port 8080 is already in use" on its error stream and keeps running past its 2 second timeout
    Then the answer contains "port 8080 is already in use"

  Scenario: The handover survives a command whose child outlives the shell
    Given a session with an empty background task pool
    When I run a command in the foreground that spawns a lasting child and keeps running past its 2 second timeout
    Then the tool answers within 20 seconds
    And the answer names a new background task

  Scenario: The adopted task is stopped like any other
    Given a session with an empty background task pool
    When I run a command in the foreground that keeps running past its 2 second timeout
    And I stop the adopted background task
    Then the adopted task is stopped
    And the adopted task is no longer running

  Scenario: A command that finishes in time still answers with its own output
    Given a session with an empty background task pool
    When I run a command in the foreground that prints "quick answer"
    Then the answer contains "quick answer"
    And no background task was created
