Feature: A model listing that lets a client size its request
  GET /v1/models says, for every row, the output cap the row runs with and the
  request parameters the endpoint takes, and GET /v1/models/{id} returns one
  row - so an OpenAI client learns what it needs before it posts (issue #579).

  Scenario: A configured model row carries its output cap and supported parameters
    Given a direct model server capping output at 4096 tokens
    When an OpenAI client asks for the model listing
    Then the direct model row advertises an output cap of 4096 tokens
    And the direct model row names the parameters it accepts
    And the agent row names no generation parameters

  Scenario: A client retrieves one model row by id
    Given a direct model server
    When an OpenAI client retrieves the model "local/qwen3-1.7b"
    Then the retrieved row is the direct model's listing entry

  Scenario: A request above the output cap is clamped to it
    Given a direct model server capping output at 4096 tokens
    When an OpenAI client posts a chat completion asking for 8192 output tokens
    Then the model receives a cap of 4096 tokens
