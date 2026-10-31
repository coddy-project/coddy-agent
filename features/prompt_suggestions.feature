Feature: Optional follow-up prompt suggestions
  After a conversation turn, the operator can accept a suggested next prompt.
  The agent setting controls whether an extra model call is made.

  Scenario: Suggestions are enabled by default and shared for a finished turn
    Given a finished conversation with prompt suggestions unset
    When two surfaces request its next prompt
    Then both surfaces offer "run the tests"
    And only one suggestion model call was made
    And the recorded conversation is unchanged

  Scenario: The operator disables prompt suggestions
    Given a finished conversation with prompt suggestions disabled
    When two surfaces request its next prompt
    Then neither surface offers a suggestion
    And no suggestion model call was made
    And the recorded conversation is unchanged

  Scenario: A suggestion is available after the first completed reply
    Given a conversation with its first completed reply
    When two surfaces request its next prompt
    Then both surfaces offer "run the tests"
    And only one suggestion model call was made
    And the recorded conversation is unchanged

  Scenario: The suggestion call forks the main agent context
    Given a finished conversation with a main agent context
    When two surfaces request its next prompt
    Then both surfaces offer "run the tests"
    And the prediction request inherits the complete main context
    And only one suggestion model call was made
    And the recorded conversation is unchanged
