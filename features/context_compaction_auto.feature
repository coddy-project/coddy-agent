Feature: Automatic context compaction
  When the estimated context usage reaches the configured percent of the
  model's context window (compaction.threshold_percent, default 80), the agent
  compacts older history automatically before calling the model, so long
  sessions keep fitting the window without manual /compact commands. Recent
  turns (compaction.keep_recent_turns) stay verbatim.

  Scenario: A prompt over the threshold compacts history before the reply
    Given a running coddy HTTP server with a summarizing agent and a tiny context window
    And an HTTP session with 4 completed exchanges
    When the user sends a regular prompt
    Then the agent reply arrives over HTTP
    And the session transcript contains a compaction summary row
    And the session transcript still contains all 4 original exchanges
    And HTTP session stats match the compacted LLM context
