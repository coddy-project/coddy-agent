Feature: A model narrows the tools it is offered
  A models[] row may carry tools (an allowlist) and disallowed_tools (a
  denylist). While a session runs on that model, the request's tool list and
  the tool list printed in the system prompt hold exactly the tools the lists
  leave, so a small-context local model is not sent the schemas of tools it
  never calls, while another model on the same configuration keeps every tool.
  A call to a tool the model was not offered is refused when it is about to
  run, with a clear result and without a permission prompt.

  Scenario: An allowlist offers exactly the listed tools
    Given a coddy session on a model whose tools are "read, grep"
    And a model that answers directly
    When the user sends a message
    Then the model is offered exactly the tools "grep, read"
    And the system prompt lists exactly the tools "grep, read"
    And the turn ends with the model's answer

  Scenario: A denylist removes tools from the full set
    Given a coddy session on a model whose disallowed tools are "write, run_command"
    And a model that answers directly
    When the user sends a message
    Then the model is offered the full set of tools without "write, run_command"
    And the system prompt lists the same tools
    And the turn ends with the model's answer

  Scenario: A call to a tool outside the allowlist is refused
    Given a coddy session on a model whose tools are "read, grep"
    And a model that requests the "write" tool once, then answers
    When the user sends a message
    Then the file is not written
    And the tool call is answered with the model's tool refusal
    And no permission prompt was raised
    And the turn ends with the model's answer

  Scenario: A model without tool lists keeps every tool
    Given a coddy session on a model with no tool lists
    And a model that requests the "write" tool once, then answers
    When the user sends a message
    Then the model is offered the full set of tools without ""
    And the file is written
    And the turn ends with the model's answer
