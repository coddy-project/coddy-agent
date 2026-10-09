Feature: Undoing the edit of a sent message restores the conversation
  Editing a sent message rewinds the session at that message. The cut tail is
  kept until the next prompt after the edited turn, so the operator can take
  the edit back and get the conversation exactly as it was before it.

  Background:
    Given a running coddy HTTP server
    And a stored session with 2 user messages

  Scenario: Undo after the edited turn brings the original turns back
    When I rewind the session at user message 1
    And I send the prompt "edited second question" to the session
    Then the transcript offers to undo the edit of user message 1
    When I undo the last rewind
    Then the transcript holds the 2 original turns
    And the transcript offers no undo
