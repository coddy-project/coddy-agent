Feature: Tool call records stay inside the session bundle
  The id of a tool call comes from the model's provider, and the bundle keeps one
  folder per call under tool_calls/. An id that no filesystem can take as a folder
  name - a traversal, a path separator, or one longer than a name may be - is kept
  under a name derived from it, with the id itself in meta.json, so the record
  never lands outside the bundle and is never lost. The turn runs to its end
  either way, and the arguments a permission prompt would resume on are readable
  under the id the provider sent.

  Scenario: An id that points outside the bundle writes nothing outside it
    Given a session bundle in its own store and a workspace file "note.txt"
    When the model reads "note.txt" under the tool call id "../../escaped"
    Then nothing is written outside the session bundle
    And the bundle holds one tool call folder
    And the tool call arguments and result are readable under the id the provider sent
    And the turn ended with the model's answer

  Scenario: An id longer than a file name keeps its record
    Given a session bundle in its own store and a workspace file "note.txt"
    When the model reads "note.txt" under a tool call id of 300 characters
    Then the bundle holds one tool call folder
    And the tool call arguments and result are readable under the id the provider sent
    And the turn ended with the model's answer

  Scenario: An ordinary provider id keeps its own folder name
    Given a session bundle in its own store and a workspace file "note.txt"
    When the model reads "note.txt" under the tool call id "call_abc123"
    Then the bundle holds the tool call folder "call_abc123"
    And the tool call arguments and result are readable under the id the provider sent
    And the turn ended with the model's answer
