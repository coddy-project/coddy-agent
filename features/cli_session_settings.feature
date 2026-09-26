Feature: Session settings on the console
  The console shows the settings of the session on screen: its footer names
  the permission mode of that session and what it has changed for its next
  turns, not those of the session the operator left. A session keeps a
  permission mode switched on in it, and what it armed for its next turns, for
  as long as the console runs, also after the operator has started another
  session and come back to it; a restart returns it to tools.permission_mode
  and forgets the overrides (#362).

  Scenario: The footer follows the session entered and the session keeps its settings
    Given a coddy console app over a stub agent runner
    When the console app starts
    And the operator switches the permission mode to "bypass"
    And the operator arms the model "stub/model-two" for the next 2 turns
    Then the footer shows the permission mode "bypass"
    And the footer shows "next 2 turns: model stub/model-two"
    When the operator starts a new session
    Then the footer shows no permission mode
    And the footer shows no turn overrides
    When the operator resumes the session before
    Then the footer shows the permission mode "bypass"
    And the footer shows "next 2 turns: model stub/model-two"
