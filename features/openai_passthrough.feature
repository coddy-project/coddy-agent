Feature: POST /v1/chat/completions passes a client's tools and images through to a direct model
  A models[].model id on the OpenAI-compatible endpoint is coddy standing in
  for the provider: the client brings its own tools and images, the model
  answers with tool calls and sees the pictures. VS Code Copilot in Agent mode
  works exactly like that. The tools travel to the provider as offered, a tool
  call comes back as OpenAI delta.tool_calls with finish_reason tool_calls, the
  client's tool result travels on the next request under its call id, and an
  image_url part reaches a vision model as an image. The agent, plan and ask
  profiles run coddy's own tools and take no tools from the client.

  Scenario: The client's tools reach the model and its tool call streams back
    Given a coddy server whose direct model is a recording stub with tool calling
    When an OpenAI client streams "local/qwen3-1.7b" with a "get_weather" tool
    Then the upstream request offered the tool "get_weather"
    And a tool_calls delta calls "get_weather" with arguments {"city":"Paris"}
    And the last chunk before [DONE] finishes with "tool_calls"

  Scenario: The client answers the tool call and receives the final answer
    Given a coddy server whose direct model is a recording stub with tool calling
    When an OpenAI client streams "local/qwen3-1.7b" with the tool result "18°C" for call "call_weather_1"
    Then the upstream request carried the tool result "18°C" under call "call_weather_1"
    And the client assembles the answer "It is 18°C in Paris."
    And the last chunk before [DONE] finishes with "stop"

  Scenario: A non-streamed answer carries the tool calls
    Given a coddy server whose direct model is a recording stub with tool calling
    When an OpenAI client posts "local/qwen3-1.7b" with a "get_weather" tool without streaming
    Then the JSON answer calls "get_weather" with arguments {"city":"Paris"}
    And the JSON answer finishes with "tool_calls"

  Scenario: An image reaches a vision model as an image
    Given a coddy server whose direct model is a recording stub with tool calling
    When an OpenAI client streams "local/qwen3-1.7b" with a text part and an image part
    Then the upstream request carried the image as an image_url part
    And the client assembles the answer "A red square."

  Scenario: The agent keeps its own tools
    Given a coddy server whose direct model is a recording stub with tool calling
    When an OpenAI client streams "agent" with a "get_weather" tool
    Then the upstream request offered coddy's own tools and not "get_weather"
    And the client assembles the answer "Hi."
