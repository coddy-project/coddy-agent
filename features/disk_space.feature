Feature: The room left on the disk is watched before it runs out
  A session is saved on every turn, so a disk that fills up costs the
  transcript long after the conversation started. Coddy reads the free space of
  the disk that holds the sessions folder, compares it with sessions.min_free_mb
  (512 MB unless the configuration says otherwise) and reports the state "low"
  under it. Once a save has actually failed for want of room the state is
  "full", whatever the numbers say, and every client is told at once without
  anything being written into the transcript that could not be saved. The
  failure ends when saves work again (issue #465).

  Scenario: A disk with room is ok
    Given the disk that holds the sessions has 4096 MB free and the warning threshold is 512 MB
    Then the storage state is "ok"

  Scenario: A disk under the threshold is low
    Given the disk that holds the sessions has 300 MB free and the warning threshold is 512 MB
    Then the storage state is "low"
    And the storage report says 300 MB are free

  Scenario: With the warning off the figures are kept and nothing is judged
    Given the disk that holds the sessions has 300 MB free and the warning threshold is 0 MB
    Then the storage state is "ok"
    And the storage report says 300 MB are free

  Scenario: A save that fails for lack of room is announced once and ends when saves work again
    Given the disk that holds the sessions has 4096 MB free and the warning threshold is 512 MB
    And a session is open
    And the disk refuses writes
    When the session is switched to plan mode
    And the session is switched to ask mode
    Then the storage state is "full"
    And clients were told once that saves are failing
    When the disk accepts writes again
    And the session is switched to agent mode
    Then clients were told that the failure is over
    And the storage state is "ok"
