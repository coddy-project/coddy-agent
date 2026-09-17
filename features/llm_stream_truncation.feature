Feature: Truncated LLM streams surface as errors
  An SSE stream that ends without a [DONE] marker and without a
  finish_reason was cut mid-generation. Passing the accumulated text off as
  a successful end_turn closes the agent turn on half an answer (issue #86),
  so the cut must surface as an error while the text already delivered to
  the caller is preserved next to it. A stream that carries a finish_reason
  but no [DONE] marker is still a complete response: not every
  OpenAI-compatible server sends the marker. The Codex Responses stream has
  terminal events of its own: response.completed ends a whole answer,
  response.incomplete names why the answer stopped short, and a stream that
  closes with neither was cut like any other.

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

  Scenario: A Codex stream cut before a terminal event fails and keeps the partial text
    Given a "codex" provider pointed at a stub server that cuts the stream after text deltas
    When a streaming completion is requested
    Then the call fails with a truncation error
    And the partial response preserves text "Hello fr"

  Scenario: A Codex stream that completes the response succeeds
    Given a "codex" provider pointed at a stub server that completes the response
    When a streaming completion is requested
    Then the call succeeds with the complete text "Hello from server"
    And the reported stop reason is "end_turn"

  Scenario: A Codex response stopped at its output cap reports max_tokens
    Given a "codex" provider pointed at a stub server that stops the response at its output cap
    When a streaming completion is requested
    Then the call succeeds with the complete text "Hello fr"
    And the reported stop reason is "max_tokens"
