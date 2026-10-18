Feature: The operator runs a command the agent was refused
  A shell command the agent needs is sometimes refused to it by a protection
  rule: a hook, the unsafe-command check, a mode without the shell. The agent
  then asks the operator to run it with operator_command. The operator sees the
  exact command, the shell and the folder, and presses Run or Decline; the
  command runs in the operator shell of the configuration, and its output is
  the tool result, so the turn reads it and goes on.

  Scenario: A command a hook refused is run by the operator and the turn reads its output
    Given an operator command session where a hook blocks run_command
    And the operator will press Run
    When the model asks for "echo operator-ok" with run_command and then with operator_command
    Then run_command was refused by the hook
    And the operator was shown the command "echo operator-ok" in the operator shell
    And the model received the output "operator-ok" with exit code 0
    And the turn ended with the model's answer

  Scenario: A command the operator declines is not run
    Given an operator command session where a hook blocks run_command
    And the operator will press Decline
    When the model asks for "echo should-not-run > declined.txt" with run_command and then with operator_command
    Then the operator was shown the command "echo should-not-run > declined.txt" in the operator shell
    And the model was told the operator declined to run the command
    And the workspace has no file "declined.txt"

  Scenario: The tool is offered only where an operator can answer
    Given an operator command session where a hook blocks run_command
    Then a turn on a surface with an operator is offered operator_command
    And a turn on a surface without an operator is not offered operator_command
