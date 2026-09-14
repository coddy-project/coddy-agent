Feature: Subagents in the web UI
  A project subagent definition travels with the checkout, so under
  subagents.project_trust ask it is refused until the operator approves it for
  the workspace. The web UI offers that approval where the operator already is:
  in Settings next to the catalog, and under the spawn_agent call that was
  refused. A detached subagent whose parent turn has ended asks for permission
  on its own task in the Tasks panel, because no chat stream is left to ask in.

  Scenario: Approve a project definition from the Subagents settings
    Then the Subagents settings tab approves a definition for the session workspace and shows it as trusted

  Scenario: Approve a refused spawn from the transcript
    Then the refused spawn_agent notice approves the definition without starting it again

  Scenario: Answer a detached subagent's permission prompt from the Tasks panel
    Then the Tasks panel answers a detached subagent's prompt against the child session
