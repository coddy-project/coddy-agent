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
  closes with neither was cut like any other. An event that arrives framed
  but ends inside its JSON was cut the same way (issue #384): the SDK
  decoders of Codex and Anthropic report it as a JSON syntax error at the end
  of the input, and the OpenAI-compatible reader, which also dispatches a last
  frame the server closed without a blank line, meets the same error on a
  body cut inside a data: line. In every case the text already delivered is
  kept, no tool call of the unfinished answer runs, and the request is not
  sent again once deltas reached the caller. A frame that carries no data is
  not an event at all: the Codex backend sends a ": keep-alive" comment while
  the model is silent, and the stream reads on past it to its terminal event
  instead of reporting it as an event cut inside its JSON.

  Scenario: A stream cut after text deltas fails and keeps the partial text
    Given an "openai" provider pointed at a stub server that cuts the stream after text deltas
    When a streaming completion is requested
    Then the call fails with a truncation error
    And the partial response preserves text "Hello fr"
    And the stub server received 1 request

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
    And the stub server received 1 request

  Scenario: A Codex event cut inside its JSON after text deltas fails and keeps the partial text
    Given a "codex" provider pointed at a stub server that cuts an event inside its JSON after text deltas
    When a streaming completion is requested
    Then the call fails with a truncation error
    And the partial response preserves text "Hello fr"
    And the stub server received 1 request

  Scenario: An OpenAI-compatible stream whose last frame ends inside its JSON fails and keeps the partial text
    Given an "openai" provider pointed at a stub server that cuts the last frame inside its JSON
    When a streaming completion is requested
    Then the call fails with a truncation error
    And the partial response preserves text "Hello fr"
    And the stub server received 1 request

  Scenario: A Codex stream that completes the response succeeds
    Given a "codex" provider pointed at a stub server that completes the response
    When a streaming completion is requested
    Then the call succeeds with the complete text "Hello from server"
    And the reported stop reason is "end_turn"

  Scenario: A Codex stream with keep-alive comments while the model is silent succeeds
    Given a "codex" provider pointed at a stub server that sends keep-alive comments while the model is silent
    When a streaming completion is requested
    Then the call succeeds with the complete text "Hello from server"
    And the reported stop reason is "end_turn"
    And the stub server received 1 request

  Scenario: A Codex response stopped at its output cap reports max_tokens
    Given a "codex" provider pointed at a stub server that stops the response at its output cap
    When a streaming completion is requested
    Then the call succeeds with the complete text "Hello fr"
    And the reported stop reason is "max_tokens"
