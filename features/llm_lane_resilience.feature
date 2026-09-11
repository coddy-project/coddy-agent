Feature: Recovering a turn on a load-balanced model lane
  One model name at a proxy is often a group of interchangeable deployments,
  and a single sick member answers nothing at all, or answers with internal
  reasoning and neither text nor a tool call. Both failures belong to the
  attempt, not to the conversation, so the loop re-issues the very same request
  once before it gives up or starts arguing with the model: the replay lands on
  a different member of the group. Nothing from the failed attempt reached the
  user, so nothing is emitted twice.

  Scenario: A deployment that never sends a first token is re-issued
    Given a coddy agent whose first-token guard fires quickly
    And a deployment that stays silent on its first call and answers on the next
    When the user sends a prompt on the lane
    Then the lane receives the same request a second time
    And the turn ends with the model's answer
    And the turn reports no error to the user

  Scenario: An empty assistant turn is re-issued before the model is nudged
    Given a coddy agent on a lane that can answer with reasoning only
    And a deployment that returns reasoning without an answer on its first call
    When the user sends a prompt on the lane
    Then the lane receives the same request a second time
    And the retried request carries no nudge and no empty assistant turn
    And the turn ends with the model's answer
