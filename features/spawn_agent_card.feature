Feature: Spawn agent tool card
  The chat presents a delegated task as readable agent details.

  Scenario: Inspect a completed agent invocation
    Then the spawn agent card shows its identity, description, multiline prompt, timeout and result
