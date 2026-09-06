Feature: Operator hooks around subagent runs and permission prompts
  Hooks follow the agent into its children: a tool hook fires inside a child session with
  the child's identity in the payload, a SubagentStart hook can refuse a spawn or hand the
  child context, a SubagentStop hook sees the child's report, and a Notification hook learns
  that a permission prompt is waiting for the operator.

  @subagent
  Scenario: Tool hooks fire inside a child session with the subagent identity
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And the workspace definition "reviewer" is approved for that workspace
    And the operator's hooks.json has a PreToolUse hook for "run_command" that records its stdin
    And a parent agent session in that workspace
    When the parent model spawns "reviewer" in the foreground and the child runs a command before answering "REPORT: ran"
    Then the recorded payload names the event "PreToolUse" and the tool "run_command"
    And the recorded payload carries the subagent "reviewer" spawned by the parent session

  @subagent
  Scenario: A SubagentStart hook hands the child context
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And the workspace definition "reviewer" is approved for that workspace
    And the operator's hooks.json has a SubagentStart hook for "reviewer" that adds the context "answer in one line"
    And a parent agent session in that workspace
    When the parent model spawns "reviewer" in the foreground and the child answers "REPORT: short"
    Then the child model's task carried the context "answer in one line"
    And the spawn_agent tool result contains "REPORT: short"

  @subagent
  Scenario: A SubagentStart hook refuses a spawn
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And the workspace definition "reviewer" is approved for that workspace
    And the operator's hooks.json has a SubagentStart hook for "reviewer" that blocks with the reason "no delegation today"
    And a parent agent session in that workspace
    When the parent model spawns "reviewer" in the foreground and the child answers "REPORT: never"
    Then the spawn_agent tool result says the spawn was blocked by a hook with the reason "no delegation today"
    And no child session was created

  @subagent
  Scenario: A SubagentStop hook sees the child's report
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And the workspace definition "reviewer" is approved for that workspace
    And the operator's hooks.json has a SubagentStop hook for "reviewer" that records its stdin
    And a parent agent session in that workspace
    When the parent model spawns "reviewer" in the foreground and the child answers "REPORT: two findings"
    Then the recorded payload names the event "SubagentStop" for the subagent "reviewer"
    And the recorded payload carries a report mentioning "REPORT: two findings"
    And the recorded payload carries the status "end_turn"

  @notification
  Scenario: A Notification hook learns that a permission prompt is waiting
    Given the operator's hooks.json has a Notification hook for "permission_prompt" that records its stdin
    And an agent session in permission mode "ask"
    And the client answers permission requests with "allow"
    When the model runs the command "echo notify"
    Then the recorded payload names the event "Notification" with the type "permission_prompt"
    And the recorded payload carries the command "echo notify" as the tool input
    And the tool result contains "notify"
