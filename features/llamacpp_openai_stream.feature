Feature: llama.cpp server works as an OpenAI-compatible streaming provider
  A "type: openai" provider pointed at a llama.cpp server must survive the
  llama.cpp SSE dialect: keep-alive comments, empty data frames, and the
  non-standard "error:" field used for mid-stream errors by 2025-era builds.
  The real server message must reach the operator instead of the SSE
  decoder's "unexpected end of JSON input".

  Scenario: Streaming completion survives llama.cpp framing quirks
    Given an "openai" provider pointed at a stub llama.cpp server streaming a completion with framing quirks
    When a streaming completion is requested
    Then the call succeeds with text "Hello from llama.cpp"
    And the reasoning delta "Thinking." is streamed
    And the stop reason is "end_turn"
    And usage reports 12 input and 5 output tokens

  Scenario: Streaming tool call survives llama.cpp framing quirks
    Given an "openai" provider pointed at a stub llama.cpp server streaming a tool call
    When a streaming completion is requested
    Then the call succeeds with a "get_weather" tool call with arguments {"city":"Paris"}
    And the stop reason is "tool_use"

  Scenario: Mid-stream llama.cpp error frame surfaces the server message
    Given an "openai" provider pointed at a stub llama.cpp server that fails mid-stream with an "error:" frame
    When a streaming completion is requested
    Then the call fails with an error containing "exceeds the available context size"
    And the error does not contain "unexpected end of JSON input"
