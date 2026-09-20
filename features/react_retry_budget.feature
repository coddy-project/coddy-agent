Feature: ReAct respects the configured retry allowance
  An operator's retry limit covers transport retries and recovery from an
  unanswered model request, rather than giving each layer a fresh allowance.

  Scenario: Disabling retries sends a signed reasoning-only request just once
    Given a ReAct agent with retries disabled
    When the model returns signed reasoning with no answer
    Then exactly one upstream request was sent
    And the turn reports that the model produced no reply
    And the signed reasoning remains in the transcript

  Scenario: An allowed recovery does not replay empty assistant content
    Given a ReAct agent with two retries available
    When the model answers after two reasoning-only responses
    Then exactly three upstream requests were sent
    And the recovery requests contain no empty assistant text
    And the turn finishes with the answer
