Feature: OpenAI request body limit
  A request below the configured limit reaches the model and returns its answer.

  Scenario: A chat completion below the configured limit succeeds
    Given a direct model server with a 1024 byte request body limit
    When an OpenAI client posts a chat completion below the limit
    Then the model receives the request and the client receives its answer
