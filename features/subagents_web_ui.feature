Feature: Subagents in the web UI
  Settings -> Subagents lists the definitions a session in this workspace can
  spawn and what each one declares. The list only reads: a project definition
  still awaiting approval says so, and names the command that approves it on the
  machine running coddy.

  A background subagent outlives the turn that spawned it, so when it needs a
  permission there is no chat stream left to carry the prompt. The chat of its
  parent session - the conversation the person is reading - shows the prompt at
  the end, naming the subagent, and the answer goes to the child session that is
  waiting. Denying refuses that one call; the subagent carries on.

  Scenario: The Subagents settings tab lists the workspace's definitions
    Then the Subagents settings tab lists the definitions of the session workspace with their scope, description and file

  Scenario: A background subagent's prompt is answered in its parent chat
    Then a background subagent's prompt waits at the end of its parent chat
    And the parent chat answers that prompt against the child session

  Scenario: A late permission stays visible to a reader following the conversation
    Then a new background permission follows a reader at the bottom without interrupting a reader of older messages
