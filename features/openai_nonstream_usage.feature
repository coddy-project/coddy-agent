Feature: Non-streaming chat completions report known token usage
  An OpenAI-compatible client needs the model's token counts on the JSON
  response to account for a request without asking for a stream.

  Scenario: A direct model reports its token usage
    Given a coddy server with a model that reports token usage
    When an OpenAI client requests a non-streaming completion from "local/qwen3-1.7b"
    Then the JSON completion reports 12 prompt and 5 completion tokens
    And its prompt token details report 4 cached tokens

  Scenario: An agent profile reports its token usage
    Given a coddy server with a model that reports token usage
    When an OpenAI client requests a non-streaming completion from "agent"
    Then the JSON completion reports 12 prompt and 5 completion tokens
    And its prompt token details report 4 cached tokens
