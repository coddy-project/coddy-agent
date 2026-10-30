Feature: Spawn agent tool card
  The chat presents a delegated task as readable agent details.

  Scenario: Inspect a completed agent invocation
    Then the spawn agent card shows its identity, description, multiline prompt, timeout and result

  Scenario: The card offers what its prompt needs and leaves the transcript to the task
    Then a prompt that fits has no More control
    And the More control follows the prompt's overflow as the card resizes
    And the card of a spawn does not lead to the transcript
