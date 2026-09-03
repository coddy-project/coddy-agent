Feature: The agent delegates bounded work to subagents
  A long investigation or a parallel fan-out fills the one conversation the operator is
  watching. A subagent is a child agent run with its own context window, its own session
  bundle and a role the operator wrote in a markdown file; the parent gets back only the
  child's final report. Subagent runs are tasks in the background task pool, so the pool
  tools, the Tasks panel and the REST surface all see them.

  Scenario: A project-local definition is offered to the model
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And a parent agent session in that workspace
    When the parent model receives its first request
    Then the system prompt lists the subagent "reviewer" with its description
    And the model is offered the spawn_agent tool

  Scenario: A foreground spawn returns the child's report and persists the child session
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And a parent agent session in that workspace
    And the workspace definition "reviewer" is approved for that workspace
    When the parent model spawns "reviewer" in the foreground and the child answers "REPORT: two findings"
    Then the spawn_agent tool result contains "REPORT: two findings"
    And the tool result names the child session
    And the child session bundle records the parent session id and the subagent name "reviewer"
    And the pool recorded a finished task of kind "agent" for the parent session

  Scenario: A background spawn is a pool task whose report is collected later
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And a parent agent session in that workspace
    And the workspace definition "reviewer" is approved for that workspace
    When the parent model spawns "reviewer" in the background and the child answers "REPORT: done"
    Then the spawn_agent tool result reports a background task id
    And the pool lists a running or finished task of kind "agent" for the parent session
    When the parent waits for that task with background_wait
    Then the background_wait result contains "REPORT: done"

  Scenario: Child updates never reach the parent's client
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And a parent agent session in that workspace
    And the workspace definition "reviewer" is approved for that workspace
    When the parent model spawns "reviewer" in the foreground and the child answers "REPORT: quiet"
    Then the parent's client received the spawn_agent tool call
    And the parent's client received no message chunk containing "REPORT: quiet"

  Scenario: A child at the depth limit is not offered spawn_agent
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And a parent agent session in that workspace
    And the workspace definition "reviewer" is approved for that workspace
    When the parent model spawns "reviewer" in the foreground and the child answers "REPORT: leaf"
    Then the child model was not offered the spawn_agent tool
    And the child model was not offered the question tool

  Scenario: A definition can only narrow the permission mode
    Given a workspace with a subagent definition "bold" under .coddy/agents asking for permission mode "bypass"
    And a parent agent session in that workspace with permission mode "ask"
    And the workspace definition "bold" is approved for that workspace
    When the parent model spawns "bold" in the foreground and the child answers "REPORT: narrowed"
    Then the child session ran with permission mode "ask"

  Scenario: An unapproved project definition is refused with the approval hint
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And a parent agent session in that workspace
    When the parent model spawns "reviewer" in the foreground and the child answers "REPORT: never"
    Then the spawn_agent tool result says the definition is not approved for this workspace
    And the spawn_agent tool result mentions "coddy agents trust reviewer"
    And no child session was created

  Scenario: A parent running without permission prompts cannot bypass project trust
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And a parent agent session in that workspace with permission mode "bypass"
    When the parent model spawns "reviewer" in the foreground and the child answers "REPORT: never"
    Then the spawn_agent tool result says the definition is not approved for this workspace
    And no child session was created

  Scenario: A recorded receipt lets the same file spawn
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And a parent agent session in that workspace
    When the operator trusts the subagent "reviewer" for that workspace
    And the parent model spawns "reviewer" in the foreground and the child answers "REPORT: trusted"
    Then the spawn_agent tool result contains "REPORT: trusted"

  Scenario: Denied project definitions are never loaded
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And the subagents project trust policy is "deny"
    And a parent agent session in that workspace
    When the parent model receives its first request
    Then the system prompt does not list the subagent "reviewer"
    And the subagent catalog for that workspace does not name "reviewer"

  Scenario: A child inherits a client-supplied MCP server
    Given a workspace with a subagent definition "general" allowed everything
    And a parent agent session in that workspace with a client-supplied stdio MCP server "alpha"
    When the parent model spawns "general" in the foreground and the child answers "REPORT: mcp"
    Then the child model was offered the tool "alpha__get_token"

  Scenario: A child's permission request is answered in the parent chat while the parent turn is alive
    Given a workspace with a subagent definition "writer" under .coddy/agents
    And a parent agent session in that workspace with permission mode "ask"
    And the workspace definition "writer" is approved for that workspace
    When the parent model spawns "writer" in the foreground and the child runs a command before answering "REPORT: ran"
    Then the parent's client was asked to approve the command on behalf of subagent "writer"
    And the spawn_agent tool result contains "REPORT: ran"

  Scenario: A child's permission request is denied once the parent turn ended
    Given a workspace with a subagent definition "writer" under .coddy/agents
    And a parent agent session in that workspace with permission mode "ask"
    And the workspace definition "writer" is approved for that workspace
    When the parent model spawns "writer" in the background and the child runs a command before answering "REPORT: late"
    And the parent turn ends before the child asks
    Then the child's command was refused as "permission denied by user"
    And the parent's client was not asked about the command

  Scenario: Two children asking at once are prompted one after the other
    Given a workspace with a subagent definition "writer" under .coddy/agents
    And a parent agent session in that workspace with permission mode "ask"
    And the workspace definition "writer" is approved for that workspace
    When the parent model spawns two "writer" children that each run a command before answering
    Then the parent's client never saw two permission prompts in flight at once
    And both spawn_agent tool results contain "REPORT"

  Scenario: A child spawned by a later turn is still answered after an earlier detached child's turn ended
    Given a workspace with a subagent definition "writer" under .coddy/agents
    And a parent agent session in that workspace with permission mode "ask"
    And the workspace definition "writer" is approved for that workspace
    When the parent model spawns "writer" in the background and the child waits to be released
    And the parent turn ends
    And in a new turn the parent model spawns "writer" in the foreground and the child runs a command before answering "REPORT: later"
    Then the parent's client was asked to approve the command on behalf of subagent "writer"
    And the spawn_agent tool result contains "REPORT: later"
    When the first child is released
    Then the pool recorded a finished task of kind "agent" for the parent session

  Scenario: A child session is a read-only transcript
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And a parent agent session in that workspace
    And the workspace definition "reviewer" is approved for that workspace
    When the parent model spawns "reviewer" in the foreground and the child answers "REPORT: sealed"
    And a prompt is sent to the child session from outside
    Then the prompt is refused because subagent sessions are read-only
    And the child session transcript still ends with "REPORT: sealed"

  Scenario: Work a child left running is settled before its transcript is sealed
    Given a workspace with a subagent definition "general" allowed everything
    And a parent agent session in that workspace
    When the parent model spawns "general" in the foreground and the child starts a background command before answering "REPORT: left running"
    Then the spawn_agent tool result contains "REPORT: left running"
    And the child session owns no running task
    And the child session is a read-only transcript

  Scenario: The concurrency cap refuses the extra spawn and names the limit
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    And the subagents concurrency limit is 1
    And a parent agent session in that workspace
    And the workspace definition "reviewer" is approved for that workspace
    When the parent model spawns "reviewer" in the background and the child waits to be released
    And the parent model spawns "reviewer" again in the background
    Then the second spawn_agent tool result mentions "subagents.max_concurrent"
    When the first child is released
    Then the pool recorded a finished task of kind "agent" for the parent session
