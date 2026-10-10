Feature: Anthropic tool calls written after text or thinking
  The Messages API numbers the content blocks of an answer from zero, and a
  text or a thinking block takes an index like any other, so a tool call the
  model writes after a sentence or after its thinking is not block zero. It is
  still a call: Coddy delivers every tool call of the answer, in the order the
  model wrote them, and every call announced to the transcript as a pending row
  is delivered, so that its result can close the row.

  Scenario: A tool call written after a sentence of text
    Given an Anthropic answer that says "Let me look."
    And the answer then calls "read" with {"path":"a.go"}
    When Coddy streams the answer
    Then the answer keeps the text "Let me look."
    And the answer carries these tool calls in order
      | name | arguments       |
      | read | {"path":"a.go"} |
    And every announced tool call is delivered

  Scenario: A tool call written after the model's thinking
    Given an Anthropic answer that thinks "I should read it."
    And the answer then calls "read" with {"path":"a.go"}
    When Coddy streams the answer
    Then the answer keeps the thinking "I should read it."
    And the answer carries these tool calls in order
      | name | arguments       |
      | read | {"path":"a.go"} |
    And every announced tool call is delivered

  Scenario: Several tool calls written after text keep the order the model wrote them
    Given an Anthropic answer that says "Both."
    And the answer then calls "read" with {"path":"a.go"}
    And the answer then calls "glob" with {"pattern":"*.go"}
    And the answer then calls "read" with {"path":"c.go"}
    When Coddy streams the answer
    Then the answer carries these tool calls in order
      | name | arguments          |
      | read | {"path":"a.go"}    |
      | glob | {"pattern":"*.go"} |
      | read | {"path":"c.go"}    |
    And every announced tool call is delivered
