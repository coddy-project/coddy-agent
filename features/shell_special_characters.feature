Feature: Shell commands carry special characters verbatim
  run_command hands a command to the host shell without the shell re-parsing it as a quoted
  string, so an agent can pass backticks, quotes, asterisks, parentheses and non-ASCII text
  straight through. On Windows the command reaches PowerShell as a script file rather than a
  -Command argument, which is what used to fail with a ParserError; on Linux and macOS the
  POSIX shell is invoked exactly as before. Commands that fail are still reported as failed.

  Scenario: A command printing shell special characters returns them verbatim
    Given the host shell detected by the agent
    When I run a command that prints the literal text:
      """
      Expand `agent.qwen.md` from 5 points to **7 structured blocks** (21 items)
      """
    Then the command succeeds
    And the output contains that text verbatim

  Scenario: A command printing non-ASCII text returns it intact
    Given the host shell detected by the agent
    When I run a command that prints the literal text:
      """
      Русский текст — em dash ✅ 中文
      """
    Then the command succeeds
    And the output contains that text verbatim

  Scenario: A command that exits non-zero is reported as failed with its own exit code
    Given the host shell detected by the agent
    When I run a command that exits with code 3
    Then the command is reported as failed
    And the failure reports exit code 3
