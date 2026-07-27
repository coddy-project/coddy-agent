Feature: Shell commands carry special characters verbatim
  run_command embeds the command text untouched - nothing is escaped, quoted, or stripped -
  so backticks, quotes, asterisks, parentheses and non-ASCII text keep the meaning the host
  shell would give them at a prompt. On Windows the command is wrapped in a prologue that
  switches the output encoding to UTF-8, so results no longer come back in the console OEM
  code page, and an epilogue that reports the command's own exit code instead of collapsing
  every failure to 1. On Linux and macOS the POSIX shell is invoked exactly as before.

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
