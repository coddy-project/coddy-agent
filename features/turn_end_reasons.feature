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

  A step can also stop at the limit in the middle of what it was writing. Text
  the user already read is kept, and the model is asked to go on exactly where
  it stopped. A tool call whose arguments were cut off is never run - half a
  file written is worse than none, and the call could not be replayed to the
  provider anyway: the call is dropped, and the model is asked to make it again
  as several smaller calls. A text and the step that went on from it are two
  assistant messages of the transcript, stored as they streamed; the history of
  a later turn sends them as the one text they are, because chat templates that
  insist on alternation refuse two assistant messages in a row, every turn after.
  The answer a turn hands over - the report of a subagent, the reply of a
  non-streaming request, what a Stop hook is told - is the whole sentence, not
  the part after the cut.

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

  Scenario: A reply cut off at the output limit in the middle of its text is continued
    Given a model whose first reply is cut off at the output limit after "The notes hold"
    And an agent with no step limit configured
    When the user sends a prompt
    Then the turn ends with the rest of the sentence
    And the model was called 2 times
    And the transcript keeps "The notes hold" that the user already saw
    And the second request carried that part and asked the model to continue exactly where it stopped
    And the session's log carries no notice about the output limit

  Scenario: The next turn sends a continued answer as the one text it is
    Given a model whose first reply is cut off at the output limit after "The notes hold"
    And an agent with no step limit configured
    When the user sends a prompt
    And the user sends another prompt
    Then the request of the second turn carries the sentence as one assistant message

  Scenario: A continued answer is handed over whole
    Given a model whose first reply is cut off at the output limit after "The notes hold"
    And an agent with no step limit configured
    When the user sends a prompt
    Then the turn ends with the rest of the sentence
    And the answer the turn hands over is the whole sentence

  Scenario: A tool call cut off at the output limit is not run and is asked again in smaller pieces
    Given a model whose first step is a write call cut off at the output limit
    And an agent with no step limit configured
    When the user sends a prompt
    Then the turn ends with the model's answer
    And the model was called 2 times
    And the file the cut-off call was writing does not exist
    And the second request asked the model to make the call again in smaller pieces
    And the second request carries no half-written call
    And the session's log carries no notice about the output limit
