Feature: Hook files inside the workspace need approval before they run
  A workspace can be someone else's checkout, and <workspace>/.coddy/hooks.json (or the
  Claude Code settings file next to it) travels with it: the repository picks the commands
  Coddy would run before every tool call, with the operator's own permissions. Coddy
  therefore treats hook files found inside the workspace as untrusted. They are parsed and
  listed, but none of their hooks runs until the operator approves that exact file for that
  workspace, and editing the file withdraws the approval. The operator's own
  ~/.coddy/hooks.json is unaffected.

  @acp
  Scenario: A project hook does not run until its file is approved
    Given the workspace's .coddy/hooks.json has a PreToolUse hook for "run_command" that records its stdin
    And an agent session
    When the model runs the command "echo held"
    Then the recording hook did not run
    And the tool result contains "held"
    And the hooks catalog lists ".coddy/hooks.json" as awaiting approval

  @acp
  Scenario: An approved project hook runs
    Given the workspace's .coddy/hooks.json has a PreToolUse hook for "run_command" that records its stdin
    And the operator approved the hook file ".coddy/hooks.json" for that workspace
    And an agent session
    When the model runs the command "echo approved"
    Then the recorded payload names the event "PreToolUse" and the tool "run_command"
    And the hooks catalog lists ".coddy/hooks.json" as trusted

  @acp
  Scenario: Rewriting an approved file withdraws the approval
    Given the workspace's .coddy/hooks.json has a PreToolUse hook for "run_command" that records its stdin
    And the operator approved the hook file ".coddy/hooks.json" for that workspace
    And the workspace's .coddy/hooks.json is rewritten with a PreToolUse hook for "*" that records its stdin
    And an agent session
    When the model runs the command "echo rewritten"
    Then the recording hook did not run
    And the hooks catalog lists ".coddy/hooks.json" as awaiting approval

  @acp
  Scenario: A Claude Code settings file is held the same way
    Given the workspace's .claude/settings.json has a PreToolUse hook for "Bash" that records its stdin
    And an agent session
    When the model runs the command "echo claude"
    Then the recording hook did not run
    And the hooks catalog lists ".claude/settings.json" as awaiting approval

  @acp
  Scenario: The policy allow runs project hooks without a receipt
    Given the workspace's .coddy/hooks.json has a PreToolUse hook for "run_command" that records its stdin
    And the hooks project trust policy is "allow"
    And an agent session
    When the model runs the command "echo allowed"
    Then the recorded payload names the event "PreToolUse" and the tool "run_command"

  @acp
  Scenario: The policy deny never reads project files
    Given the workspace's .coddy/hooks.json has a PreToolUse hook for "run_command" that records its stdin
    And the hooks project trust policy is "deny"
    And an agent session
    When the model runs the command "echo denied"
    Then the recording hook did not run
    And the hooks catalog does not list ".coddy/hooks.json"

  @acp
  Scenario: The operator learns about a held file from the session
    Given the workspace's .coddy/hooks.json has a PreToolUse hook for "run_command" that records its stdin
    And an agent session
    When the model runs the command "echo notice"
    Then the session's UI log notes that ".coddy/hooks.json" awaits approval and names "coddy hooks trust"
    And the note is recorded once even after a second turn

  @http
  Scenario: The catalog and the approval routes over HTTP
    Given a running coddy HTTP server
    And the workspace's .coddy/hooks.json has a PreToolUse hook for "run_command"
    When I list the hooks
    Then the hooks list shows ".coddy/hooks.json" as awaiting approval with a PreToolUse hook for "run_command"
    When I approve the hook file ".coddy/hooks.json"
    And I list the hooks
    Then the hooks list shows ".coddy/hooks.json" as trusted
    When I revoke the hook file ".coddy/hooks.json"
    And I list the hooks
    Then the hooks list shows ".coddy/hooks.json" as awaiting approval
