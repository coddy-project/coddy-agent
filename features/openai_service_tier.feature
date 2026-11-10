Feature: OpenAI model controls
  Scenario: Astra uses Responses with an independent service tier
    Given an API-key Astra model with priority service
    When the model answers a prompt
    Then the request uses Responses with priority service
