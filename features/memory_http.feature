Feature: The HTTP surface shows memory runs as system agent tasks
  A memory run is a background task of the session that started it, so the Tasks drawer
  already polls it. The REST surface marks the task as a system agent named memory, names
  the child session that holds its transcript, serves that transcript read-only like any
  child's, and carries nothing else about memory in the messages payload.

  Scenario: A memory run is an agent task row flagged as a system task
    Given a running coddy serve server with a session
    And that session started a memory run backed by child session "sess_bdd_memory"
    When I GET the background tasks of that session
    Then the response lists a task of kind "agent"
    And that task row names the agent "memory" and the child session "sess_bdd_memory"
    And that task row is flagged as a system task

  Scenario: The memory child's transcript is readable and the messages payload carries no memory rows
    Given a running coddy serve server with a session
    And a live memory child session "sess_bdd_memlive" of that session whose transcript says "Already on disk: the API returns JSON"
    When I GET the messages of "sess_bdd_memlive"
    Then the messages contain "Already on disk: the API returns JSON"
    And the messages payload is read-only and links the parent session
    When I GET the messages of the parent session
    Then the messages payload carries no "memoryTurns" field
