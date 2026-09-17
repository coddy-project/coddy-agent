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

  Scenario: A TLS handshake that times out before the request is sent is retried and succeeds
    Given an "openai" provider whose upstream leaves the first TLS handshake unanswered and then streams a completion
    When a streaming completion is requested
    Then the call succeeds with text "Hello after retry" over 2 upstream connections
    And 1 request reached the upstream handler

  Scenario: An HTTP/2 stream reset before any output is retried and succeeds
    Given an "openai" provider whose upstream resets the first HTTP/2 stream before any output and then streams a completion
    When a streaming completion is requested
    Then the call succeeds with text "Hello after retry" in 2 upstream requests

  Scenario: A connection whose peer stops answering pings is closed and the request is retried
    Given an "openai" provider whose upstream goes silent on the first connection, pings included, and then streams a completion
    When a streaming completion is requested
    Then the call succeeds with text "Hello after retry" over 2 upstream connections
    And 2 requests reached the upstream handler
