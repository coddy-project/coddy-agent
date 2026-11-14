Feature: Reading working-copy edits without per-file Git processes
  Reading a session's edits preserves both sides of every changed file and
  ignores edits outside the session folder. Ordinary tracked files are read
  with a fixed number of Git processes rather than one process per file.

  Scenario: More changed files do not require more Git processes
    When I read a session subfolder with 5 modified files
    And I read another session subfolder with 25 modified files
    Then both reads use the same number of Git processes, at most 6
