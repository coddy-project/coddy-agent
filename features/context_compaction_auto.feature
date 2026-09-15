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

  # coddy-project/coddy-agent#245: a model entry without max_context_tokens.
  # The web UI draws its context ring against the window GET /v1/models
  # reports, so the trigger has to measure against that same window.
  Scenario: A model without max_context_tokens compacts at the window its provider reports
    Given a running coddy HTTP server whose model has no max_context_tokens and whose provider reports a tiny context window
    And an HTTP session with 4 completed exchanges
    When the user sends a regular prompt
    Then the agent reply arrives over HTTP
    And the session transcript contains a compaction summary row
    And the session transcript still contains all 4 original exchanges
    And the model list reports the provider's context window for the model

  Scenario: A provider window arriving after the model-list deadline reaches the stream
    Given a running coddy HTTP server with a delayed provider window
    And an HTTP session with 4 completed exchanges
    When the model list returns the fallback window before the provider answers
    And the provider finishes reporting its context window
    And the user sends a streaming compaction command
    Then the usage update reports the late provider window
