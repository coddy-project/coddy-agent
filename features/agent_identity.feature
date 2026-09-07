Feature: Coddy identifies itself to the gateway
  A gateway in front of the model cannot tell one OpenAI-compatible client from
  another by the wire protocol, so it attributes traffic by matching the opening
  of the system prompt against a table of known products. Every request Coddy
  sends therefore names the product at the start of its system prompt, whichever
  template the session runs on, and names it exactly once.

  Scenario Outline: The request Coddy sends opens by naming the product
    Given a session in "<mode>" mode using "<templates>" prompt templates
    When the agent sends a turn to the model
    Then the system prompt of that request names Coddy in its opening
    And the system prompt names Coddy exactly once

    Examples:
      | mode  | templates |
      | agent | built-in  |
      | plan  | built-in  |
      | agent | custom    |
      | plan  | custom    |
