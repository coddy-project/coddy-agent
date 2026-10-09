Feature: Reading a todo plan that does not exist
  An empty tool result gives a model nothing to act on, and a model that gets one
  calls the same tool again with the same arguments until the loop guard stops the turn.

  Scenario: The model reads the plan of a session that has none
    Given a session without a todo plan
    When the model calls coddy_todo_plan_read with no arguments
    Then the answer is not empty
    And the answer says there is no active todo plan
    And the answer names coddy_todo_plan_replace as the way to start one
