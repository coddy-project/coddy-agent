Feature: Read-only ask mode
  Ask mode answers questions from the repository and the web without changing
  anything. The model is offered only the read-only tools, and a call that
  names a hidden tool anyway - for example one echoed from history recorded in
  agent mode - is refused when it is about to run, so the read-only promise
  holds even when the model does not respect the tool list it was given.

  Scenario: The model is offered only read-only tools
    Given a coddy session in "ask" mode
    And a model that answers directly
    When the user asks a question
    Then every tool offered to the model is read-only
    And the turn ends with the model's answer

  Scenario: A hidden tool call is refused instead of executed
    Given a coddy session in "ask" mode
    And a model that requests the "write" tool once, then answers
    When the user asks a question
    Then the file is not written
    And the tool call is answered with the ask-mode refusal
    And the turn ends with the model's answer

  Scenario: The same call in agent mode writes the file
    Given a coddy session in "agent" mode
    And a model that requests the "write" tool once, then answers
    When the user asks a question
    Then the file is written
    And the turn ends with the model's answer
