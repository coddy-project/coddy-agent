Feature: Rewinding a session at a user message truncates history in place
  Editing a sent message rewinds the conversation inside the same session: the
  message and everything after it is dropped, so the edited text continues the
  same session rather than forking a new one.

  Background:
    Given a running coddy HTTP server
    And a stored session with 2 user messages

  Scenario: The session keeps only its prefix after a rewind
    When I rewind the session at user message 1
    Then the session still serves its messages
    And the transcript keeps only the first turn

  Scenario: A prompt after a rewind continues the same session
    When I rewind the session at user message 1
    And I send the prompt "edited second question" to the session
    Then the transcript shows "edited second question" after the first turn
