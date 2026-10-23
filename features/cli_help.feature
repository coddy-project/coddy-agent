Feature: Asking the binary what to type
  `coddy help` and `coddy help <command>` are what people type first when a
  binary is new to them, and only `-h` / `--help` printed the usage before
  issue #453. `coddy help` prints the same command list `coddy --help` prints,
  and `coddy help <command> ...` answers what `coddy <command> ... --help`
  answers, so the first question needs no memory of the flag conventions.

  Scenario: coddy help prints the command list
    When the operator runs "coddy help"
    Then the command succeeds
    And the output mentions "serve"
    And the output mentions "help"

  Scenario: coddy help prints the usage of one command
    When the operator runs "coddy help serve"
    Then the command succeeds
    And the output mentions "daemon"

  Scenario: every top-level command answers coddy help
    When the operator runs "coddy help" for every top-level command
    Then every command answered with its usage
