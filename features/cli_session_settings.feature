Feature: Session settings on the console
  The console shows the settings of the session on screen: its footer names
  the permission mode of that session and what it has changed for its next
  turns, not those of the session the operator left. A new session starts in
  the permission mode chosen last and keeps its own: a mode switched on in one
  session stays with it after the operator has started another session,
  switched that one and come back, and across a restart, since it is part of
  the session's metadata (#512); what a session armed for its next turns lasts
  as long as the console runs (#362).

  Scenario: The footer follows the session entered and the session keeps its settings
    Given a coddy console app over a stub agent runner
    When the console app starts
    And the operator submits the prompt "first work"
    And the stub turn streams the text "done"
    And the operator switches the permission mode to "bypass"
    And the operator arms the model "stub/model-two" for the next 2 turns
    Then the footer shows the permission mode "bypass"
    And the footer shows "next 2 turns: model stub/model-two"
    When the operator starts a new session
    Then the footer shows the permission mode "bypass"
    And the footer shows no turn overrides
    When the operator switches the permission mode to "accept_edits"
    Then the footer shows the permission mode "accept_edits"
    When the operator resumes the session before
    Then the footer shows the permission mode "bypass"
    And the footer shows "next 2 turns: model stub/model-two"
