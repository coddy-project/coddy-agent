Feature: A toolset narrows the tools a session is offered
  A toolsets[] preset names an allowlist (tools) and a denylist
  (disallowed_tools). A model names the preset its sessions run with
  (models[].toolset), and a session may pick another, or default for none
  (/toolset). While a session runs with a preset, the request's tool list and
  the tool list printed in the system prompt hold exactly the tools it leaves,
  so a small-context local model is not sent the schemas of tools it never
  calls, while another model on the same configuration keeps every tool. A call
  to a tool the request did not offer is refused when it is about to run, with
  a clear result and without a permission prompt.

  Scenario: A toolset's allowlist offers exactly the listed tools
    Given a coddy session on a model whose toolset offers "read, grep"
    And a model that answers directly
    When the user sends a message
    Then the model is offered exactly the tools "grep, read"
    And the system prompt lists exactly the tools "grep, read"
    And the turn ends with the model's answer

  Scenario: A toolset's denylist removes tools from the full set
    Given a coddy session on a model whose toolset disallows "write, run_command"
    And a model that answers directly
    When the user sends a message
    Then the model is offered the full set of tools without "write, run_command"
    And the system prompt lists the same tools
    And the turn ends with the model's answer

  Scenario: A call to a tool outside the toolset is refused
    Given a coddy session on a model whose toolset offers "read, grep"
    And a model that requests the "write" tool once, then answers
    When the user sends a message
    Then the file is not written
    And the tool call is answered with the toolset's refusal
    And no permission prompt was raised
    And the turn ends with the model's answer

  Scenario: A session that picks default is offered every tool
    Given a coddy session on a model whose toolset offers "read, grep"
    And the session picked the toolset "default"
    And a model that requests the "write" tool once, then answers
    When the user sends a message
    Then the model is offered the full set of tools without ""
    And the file is written
    And the turn ends with the model's answer

  Scenario: A model without a toolset keeps every tool
    Given a coddy session on a model without a toolset
    And a model that requests the "write" tool once, then answers
    When the user sends a message
    Then the model is offered the full set of tools without ""
    And the file is written
    And the turn ends with the model's answer
