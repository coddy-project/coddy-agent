Feature: OpenAI-compatible errors
  Scenario: An unknown model returns a typed JSON error
    When an OpenAI client requests an unknown model
    Then it receives a JSON model_not_found error with status 404
