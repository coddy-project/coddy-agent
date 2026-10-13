Feature: A met goal hands the next task to a fresh session
  /goal --then-file names a file the worker writes the next task into. When the
  supervisor finds the goal met, the text of that file becomes the goal of a
  new session in the same workspace, which starts working on it at once. The
  file is renamed so it runs only once, and the two sessions name each other.

  Scenario: A goal with a follow-up file continues in a new session
    Given a session whose worker writes "write the release notes" to ".coddy/next-task.md" in its first turn
    When the operator sends "/goal --then-file .coddy/next-task.md ship the fix"
    Then a new session in the same workspace works on the goal "write the release notes"
    And its goal keeps the follow-up file ".coddy/next-task.md"
    And its first message is the goal kickoff
    And ".coddy/next-task.md" was renamed so it never runs twice
    And each session names the other one

  Scenario: The chain ends when no follow-up file is left
    Given a session whose worker writes "write the release notes" to ".coddy/next-task.md" in its first turn
    When the operator sends "/goal --then-file .coddy/next-task.md ship the fix"
    Then the goal of the second session is complete
    And no third session was opened
    And the second session says the chain ended because ".coddy/next-task.md" does not exist
