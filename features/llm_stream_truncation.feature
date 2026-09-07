Feature: Truncated LLM streams surface as errors
  An SSE stream that ends without a [DONE] marker and without a
  finish_reason was cut mid-generation. Passing the accumulated text off as
  a successful end_turn closes the agent turn on half an answer (issue #86),
  so the cut must surface as an error while the text already delivered to
  the caller is preserved next to it. A stream that carries a finish_reason
  but no [DONE] marker is still a complete response: not every
  OpenAI-compatible server sends the marker.

  Scenario: A stream cut after text deltas fails and keeps the partial text
    Given an "openai" provider pointed at a stub server that cuts the stream after text deltas
    When a streaming completion is requested
    Then the call fails with a truncation error
    And the partial response preserves text "Hello fr"

  Scenario: A stream with a finish_reason but no [DONE] marker succeeds
    Given an "openai" provider pointed at a stub server that ends the stream with a finish_reason but no [DONE] marker
    When a streaming completion is requested
    Then the call succeeds with the complete text "Hello from server"
    And the reported stop reason is "end_turn"
