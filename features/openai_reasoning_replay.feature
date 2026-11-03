Feature: Reasoning history through an OpenAI-compatible provider
  A client can replay a reasoning-bearing assistant message to a direct model.
  Coddy preserves assistant reasoning for the upstream and returns its reasoning.

  Scenario Outline: Replay reasoning with a tool result
    Given a recording reasoning upstream and a direct Coddy model
    When a client replays assistant reasoning as "<field>" with streaming <stream>
    Then the upstream receives the reasoning and the matching tool history
    And the client receives the answer and its reasoning

    Examples:
      | field             | stream |
      | reasoning_content | false  |
      | reasoning         | false  |
      | reasoning_content | true   |
      | reasoning         | true   |
