Feature: Permission answers from an editor are honoured
  Editors that speak the Agent Client Protocol answer a permission request by nesting the
  outcome in its own object, while Coddy's own surfaces send it flat. Both shapes mean the
  same thing, so an approval from Zed must run the tool call exactly like an approval from
  the console. Reading a nested approval as a refusal is the worst possible failure: the
  operator clicks Allow, the agent is told "permission denied by user", and it reports that
  the workspace forbids shell access.

  Scenario: An editor's approval in the protocol response shape is honoured
    Given a session with no command grants
    When the agent asks permission to run "git status --short"
    And the editor answers "allow" the way the protocol nests its outcome
    Then the answer lets the command run

  Scenario: A program-wide approval from an editor widens the grant
    Given a session with no command grants
    When the agent asks permission to run "git status --short"
    And the editor answers "allow_always_program" the way the protocol nests its outcome
    Then the answer lets the command run
    And the session grants "git status"
    And running "git status -sb" no longer needs permission

  Scenario: A rejection from an editor still refuses the call
    Given a session with no command grants
    When the agent asks permission to run "git status --short"
    And the editor answers "reject" the way the protocol nests its outcome
    Then the answer refuses the command
    And running "git status --short" still needs permission
