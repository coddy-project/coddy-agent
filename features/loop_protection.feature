Feature: Runaway loop protection
  A model that stops making progress — repeating the same passage inside one
  streamed response, or requesting the same tool call with the same arguments
  over and over — is nudged once to change course. If it keeps looping the turn
  stops with a notice instead of burning the whole max_turns budget. The
  repeated text is dropped from the transcript, so it is never replayed to the
  model and cannot re-seed the loop.

  Scenario: A degenerating answer stream is cut and the turn recovers
    Given a coddy agent with the loop guard enabled
    And a model that repeats the same sentence forever, then answers normally
    When the user sends a prompt
    Then the streamed response is cut before the model stops on its own
    And the stored transcript keeps the answer without the repeated tail
    And the model is nudged once to stop repeating itself
    And the nudged request carries no repeated passage
    And the turn ends with the model's real answer

  Scenario: A looping reasoning channel is cut the same way
    Given a coddy agent with the loop guard enabled
    And a model that repeats the same thought forever with no answer text, then answers normally
    When the user sends a prompt
    Then the streamed response is cut before the model stops on its own
    And the stored transcript keeps the reasoning without the repeated tail
    And the turn ends with the model's real answer

  Scenario: Identical tool calls stop the turn instead of running to max turns
    Given a coddy agent with the loop guard enabled
    And a model that always requests the same tool call with the same arguments
    When the user sends a prompt
    Then the tool is executed fewer times than the repeat limit allows
    And every requested tool call has a result recorded
    And the turn stops with a loop notice before max turns is reached
