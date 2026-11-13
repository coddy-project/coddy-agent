Feature: Direct OpenAI request parameters
  Scenario: A client sends top_p and a developer instruction
    Given an OpenAI-compatible recording upstream and a direct model
    When a client requests a completion with top_p and a developer instruction
    Then the upstream receives top_p and the instruction as a system message
