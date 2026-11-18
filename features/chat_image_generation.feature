Feature: Native image generation in a chat
  Scenario: An explicitly enabled turn generates an assistant image
    Given an OpenAI Responses endpoint returning a generated PNG
    When the caller explicitly enables native image generation
    Then the request includes the native image generation tool
    And the response contains a typed generated image
