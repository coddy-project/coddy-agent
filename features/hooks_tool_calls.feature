Feature: Operator hooks around tool calls
  An operator attaches their own commands to the points where the agent is about to run
  a tool and where it just did. A hook is a command that reads one JSON document on
  stdin and answers with an exit code and optional JSON on stdout. Definitions live in
  the operator's own ~/.coddy/hooks.json in the shape Claude Code uses, so a hook
  written for Claude Code works unchanged.

  Scenario: A PreToolUse hook denies a command and the model reads the reason
    Given the operator's hooks.json has a PreToolUse hook for "run_command" that denies commands containing "rm -rf"
    And an agent session
    When the model runs a command containing "rm -rf" that would write the marker file "destroyed"
    Then the marker file "destroyed" does not exist
    And the tool result says the call was blocked by a hook with the reason "destructive command"
    And the client saw the tool call end as cancelled

  Scenario: A PreToolUse hook allows a command without a permission prompt
    Given the operator's hooks.json has a PreToolUse hook for "run_command" that allows every call
    And an agent session in permission mode "ask"
    When the model runs the command "echo allowed-by-hook"
    Then the tool result contains "allowed-by-hook"
    And the client received no permission request

  Scenario: A silent hook leaves the ordinary permission flow in place
    Given the operator's hooks.json has a PreToolUse hook for "run_command" that exits without a decision
    And an agent session in permission mode "ask"
    And the client answers permission requests with "allow"
    When the model runs the command "echo needs-approval"
    Then the client received a permission request for "run_command"
    And the tool result contains "needs-approval"

  Scenario: A PreToolUse hook rewrites the arguments before the tool runs
    Given the operator's hooks.json has a PreToolUse hook for "run_command" that rewrites the command to "echo rewritten-by-hook"
    And an agent session
    When the model runs the command "echo original"
    Then the tool result contains "rewritten-by-hook"
    And the tool result does not contain "original"

  Scenario: A PostToolUse hook adds feedback to the tool result
    Given the operator's hooks.json has a PostToolUse hook for "run_command" that adds the context "remember to run the tests"
    And an agent session
    When the model runs the command "echo done"
    Then the tool result contains "done"
    And the tool result contains "remember to run the tests"

  Scenario: A hook reads the session payload on stdin
    Given the operator's hooks.json has a PreToolUse hook for "*" that records its stdin
    And an agent session
    When the model runs the command "echo payload"
    Then the recorded payload names the event "PreToolUse" and the tool "run_command"
    And the recorded payload carries the session id and the workspace path
    And the recorded payload carries the command "echo payload" as the tool input

  Scenario: A hook written for Claude Code matches Coddy's tool names
    Given the operator's hooks.json has a PreToolUse hook for "Bash" that denies commands containing "rm -rf"
    And an agent session
    When the model runs a command containing "rm -rf" that would write the marker file "destroyed"
    Then the marker file "destroyed" does not exist
    And the tool result says the call was blocked by a hook with the reason "destructive command"

  Scenario: A hook that exits with code 2 blocks with its stderr as the reason
    Given the operator's hooks.json has a PreToolUse hook for "run_command" that exits with code 2 and prints "not on my watch" to stderr
    And an agent session
    When the model runs the command "echo blocked"
    Then the tool result says the call was blocked by a hook with the reason "not on my watch"

  Scenario: A PostToolUseFailure hook sees the error of a failed tool
    Given the operator's hooks.json has a PostToolUseFailure hook for "read" that records its stdin
    And an agent session
    When the model reads the missing file "nope.txt"
    Then the recorded payload names the event "PostToolUseFailure" and the tool "read"
    And the recorded payload carries an error mentioning "nope.txt"
