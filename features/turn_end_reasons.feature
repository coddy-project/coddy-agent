Feature: A turn ends for a reason the user can see, and a sick provider does not end it
  A turn that stops before the task is done has to say why (issue #255). The
  step limit agent.max_turns is off unless it is set, so a long task runs to
  its answer; when an operator does set it, a turn it stops ends with a notice
  that names the limit, next to the answer, instead of simply going quiet.

  A provider that breaks in the middle of an answer - a 500 from a proxy whose
  fallback failed, a connection cut - is a failure of the lane, not of the
  conversation (issue #246). The turn keeps the part of the answer the user
  already watched stream in, waits a moment and asks the model to go on from
  there, instead of ending and leaving the user to type "continue".

  A model with a small output limit can spend a whole step on thinking, or on a
  tool call whose arguments are cut off, and stop at max_tokens with nothing
  the agent can act on. That is no answer to end a turn on: the step is run
  again with a short message asking the model to keep its reasoning brief and
  to split large writes into smaller tool calls, and the turn goes on without
  the user typing "continue" (a subagent or a scheduled run has nobody who
  could).

  Scenario: Without a step limit a long task runs to its answer
    Given a model that reads a file 35 times before it answers
    And an agent with no step limit configured
    When the user sends a prompt
    Then the turn ends with the model's answer
    And the model was called 36 times

  Scenario: A turn stopped by its step limit says so
    Given a model that reads a file 35 times before it answers
    And an agent whose agent.max_turns is 3
    When the user sends a prompt
    Then the turn stops at its step limit
    And the session's log carries a notice that names agent.max_turns

  Scenario: A provider that fails mid-answer does not end the turn
    Given a model whose first answer breaks off with "server error 500" after "The project holds"
    And an agent with no step limit configured
    When the user sends a prompt
    Then the turn ends with the model's answer
    And the transcript keeps "The project holds" that the user already saw
    And the next request carried that part and asked the model to continue
    And the session's log carries a notice that the provider failed and the turn went on

  Scenario: A step cut off at the output limit before it produced anything does not end the turn
    Given a model whose first step is cut off at the output limit while it was still reasoning
    And an agent with no step limit configured
    When the user sends a prompt
    Then the turn ends with the model's answer
    And the model was called 2 times
    And the second request asked the model to keep its reasoning short and split large writes
    And the session's log carries no notice about the output limit
