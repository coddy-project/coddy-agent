Feature: Context compaction
  A long session is compacted: older exchanges are replaced in the LLM context
  by a generated summary while the persisted transcript keeps every original
  message. The most recent user turns stay verbatim so the agent keeps precise
  track of the current task right after compaction.

  Scenario: Compaction keeps recent turns verbatim and summarizes older ones
    Given a session with 4 completed exchanges
    When the session is compacted keeping the last 2 user turns
    Then the compaction summary is inserted into the transcript
    And the transcript still contains all 4 original exchanges
    And the next LLM request starts from the summary
    And the next LLM request contains the last 2 exchanges verbatim
    And the next LLM request does not contain the older exchanges

  Scenario: The agent continues the conversation after compaction
    Given a session with 4 completed exchanges
    And the session is compacted keeping the last 2 user turns
    When the user sends a new prompt
    Then the agent replies successfully
    And the LLM request for that reply starts from the summary

  Scenario: Compaction publishes the smaller context window over ACP
    Given a session with 4 completed exchanges
    And the ACP client has observed the context usage before compaction
    When the session is compacted keeping the last 2 user turns
    Then the ACP client receives a smaller context usage update
    And the reported ACP usage matches the compacted LLM context

  Scenario: A history too large for one summarization request is folded in passes
    Given a session with 4 completed exchanges
    And the summarizer model has room for only part of the history per request
    When the session is compacted keeping the last 2 user turns
    Then the summarizer was called more than once
    And every summarization request after the first carries the summary so far
    And the compaction summary is inserted into the transcript
    And the client was told the compaction was running

  Scenario: The model compacts the session itself
    Given a session with 4 completed exchanges
    When the model calls the compact_context tool
    Then the compaction summary is inserted into the transcript
    And the tool result says what was compacted
    And the LLM request after the tool call starts from the summary
    And every tool result in that request answers a call the request carries

  Scenario: The model compacts with the summarizer the user named
    Given a session with 4 completed exchanges
    And a second model "fake/summarizer-qwen" is configured
    When the model calls the compact_context tool with the model "qwen"
    Then the compaction summary is inserted into the transcript
    And the summary was written by "fake/summarizer-qwen"
    And the tool result names the summarizer "fake/summarizer-qwen"

  Scenario: The model compacts in the first turn of a session
    Given a session with 0 completed exchanges
    When the model calls the compact_context tool
    Then the compaction summary is inserted into the transcript
    And the summary row begins with the prompt being answered
    And the LLM request after the tool call starts from the summary
    And every tool result in that request answers a call the request carries

  # coddy-project/coddy-agent#490: a request that outgrew the window used to end
  # the turn on the provider's bare error. The turn is now compacted once and
  # asked again (features/context_in_turn_compaction.feature); this is how a turn
  # ends when the provider refuses the smaller request as well.
  Scenario: A request the provider keeps refusing as larger than the context window ends the turn with an explanation
    Given a session with 4 completed exchanges
    And the model's context window is 49152 tokens
    And the provider refuses every request of the turn: "Context limit is 49152 tokens; prompt=51402 leaves 0 output tokens, below the minimum 16"
    When the user sends a new prompt and the turn fails
    Then the turn is refused with an explanation that the context window was exceeded
    And the error says "the window is 49152 tokens"
    And the error says "the provider counted 51402 tokens against a limit of 49152"
    And the error says "Coddy compacted this turn and asked again"
    And the error says "run /compact"
    And the error says "Context limit is 49152 tokens"
    And the turn's request was sent twice, before and after the compaction

  Scenario: A summarization request the provider finds too large is asked again with less
    Given a session with 4 completed exchanges
    And the summarizer model has room for only part of the history per request
    And the summarizer refuses requests larger than its window
    When the session is compacted keeping the last 2 user turns
    Then the summarizer refused a request as too large
    And the compaction summary is inserted into the transcript
    And the transcript still contains all 4 original exchanges
