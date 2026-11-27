Feature: Incoming turn data processed by JavaScript extensions
  Scenario: Enrich incoming text and tool results
    Then enabled extensions transform incoming text and tool data on fresh and resumed streams

  Scenario: Transform assistant text without raw transport access
    Then semantic processors transform text deltas and leave other frames unchanged
