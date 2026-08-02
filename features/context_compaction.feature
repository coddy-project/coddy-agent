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
