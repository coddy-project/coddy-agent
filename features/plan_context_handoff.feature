Feature: The design plan hand-off survives a permission prompt
  Running a saved design plan hands the plan text to the agent turn, in the system
  prompt. A turn that stops on a permission prompt is not over: the user answers it
  minutes later, sometimes after the process has been restarted, and what runs then
  is the same turn. The hand-off has to still be there, or the agent carries out the
  rest of the plan having never read it.

  Scenario: The plan text is still in the prompt when the user approves the tool
    Given a session parked on a permission prompt in the middle of a plan run
    When the user approves the tool
    Then the request that continues the turn carries the plan text

  Scenario: The plan text survives a restart while the user is deciding
    Given a session parked on a permission prompt in the middle of a plan run
    And the process is restarted, so nothing is left in memory
    When the user approves the tool
    Then the request that continues the turn carries the plan text

  Scenario: The next turn does not carry the plan text again
    Given a session running a saved design plan
    When the turn finishes with no permission left pending
    Then the request of that turn carries the plan text
    And the next turn carries no plan text
