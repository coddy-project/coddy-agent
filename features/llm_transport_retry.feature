Feature: Transport failures without a status are retried
  A connection that dies before any output carries no HTTP status, yet the
  request is safe to repeat: nothing reached the caller. Coddy retries such
  transport failures within the configured retry budget instead of failing
  the turn on the first network hiccup. Once deltas were already delivered
  the same failure is not replayed, so no text is ever streamed twice.

  Scenario: A connection cut before any output is retried and succeeds
    Given an "openai" provider whose upstream cuts the connection once before any output and then streams a completion
    When a streaming completion is requested
    Then the call succeeds with text "Hello after retry" in 2 upstream requests
