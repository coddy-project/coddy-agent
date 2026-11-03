Feature: OpenAI Responses API for configured models
  POST /v1/responses answers the OpenAI Responses subset for a configured
  provider/model selector: string or item-array input, client function tools,
  a typed output list and the Responses event stream. The profile selectors
  (agent, plan, ask) keep the coddy dialect they always had.

  Scenario: a string input is answered as a Responses object
    Given a coddy server whose direct model is a recording stub with tool calling
    When an OpenAI client posts "local/qwen3-1.7b" to the Responses API with input "hello there"
    Then the answer is a response object with status "completed"
    And its output is a message item whose output_text is "Hi."
    And the response carries usage counters

  Scenario: a streamed answer follows the Responses event order
    Given a coddy server whose direct model is a recording stub with tool calling
    When an OpenAI client streams "local/qwen3-1.7b" over the Responses API with input "hello"
    Then the stream opens with response.created and response.in_progress
    And the output_text deltas assemble "Hi."
    And the stream ends with response.completed carrying the response and its usage
    And the stream has no chat.completion.chunk, [DONE] or coddy_meta frame

  Scenario: client tools and a tool result round-trip over the Responses API
    Given a coddy server whose direct model is a recording stub with tool calling
    When an OpenAI client streams "local/qwen3-1.7b" over the Responses API with a "get_weather" tool
    Then the upstream request offered the flat function tool "get_weather"
    And the stream answers with a function_call output item for "get_weather"
    When the client streams the tool result for that call over the Responses API
    Then the upstream request carried the prior call and its result
    And the client assembles the answer "It is 18°C in Paris."

  Scenario: a typed item array is replayed to the provider
    Given a coddy server whose direct model is a recording stub with tool calling
    When an OpenAI client posts "local/qwen3-1.7b" to the Responses API with a message, a function_call and a function_call_output
    Then the upstream request carried the assistant tool call and the tool result

  Scenario: the profile selector keeps the coddy stream dialect
    Given a coddy server whose direct model is a recording stub with tool calling
    When a coddy client streams "agent" over POST /v1/responses
    Then the stream carries chat.completion.chunk deltas and [DONE]
