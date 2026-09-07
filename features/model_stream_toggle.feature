Feature: A model can be configured to answer without streaming
  Some OpenAI-compatible backends stream badly or not at all behind proxies and
  gateways. Setting "stream: false" on a models[] entry makes the runtime issue
  one blocking chat completion and hand the finished answer to its caller in one
  piece, so the ReAct loop, the ACP session updates, and the SPA transcript keep
  working through the same code path they use for a live stream.

  Scenario: A blocking model answers without opening a stream
    Given an "openai" provider with streaming disabled pointed at a stub server that refuses streaming requests
    When a streaming completion is requested
    Then the call succeeds with text "Blocking answer."
    And the stub server received 1 request with "stream" unset
    And the answer arrived as a single text chunk

  Scenario: Reasoning from a blocking response reaches the caller
    Given an "openai" provider with streaming disabled pointed at a stub server that answers with reasoning
    When a streaming completion is requested
    Then the call succeeds with text "Answer after thinking."
    And the reasoning delta "Deliberating." is delivered before the answer text

  Scenario: A tool call from a blocking response reaches the caller
    Given an "openai" provider with streaming disabled pointed at a stub server that answers with a tool call
    When a streaming completion is requested
    Then the call succeeds with a "get_weather" tool call with arguments {"city":"Paris"}
    And the stop reason is "tool_use"

  Scenario: Streaming stays the transport when the key is omitted
    Given an "openai" provider with the stream key omitted pointed at a stub server that streams a completion
    When a streaming completion is requested
    Then the call succeeds with text "Streamed answer."
    And the stub server received 1 request with "stream" set to true
