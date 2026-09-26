Feature: Stable request prefix for provider prompt caching
  A provider caches a request by its prefix: the tool definitions, then the messages
  from the start. The first byte that differs from the previous request throws away
  everything cached after it, the replayed conversation included. Coddy therefore
  keeps the system message and the history byte-identical between the steps of a
  turn, and sends what moves per step - the wall clock, the todo checklist - in a
  turn context block appended after the history. Project rules do not move the
  system message either: it carries the rules that always apply, read once for the
  session, and a rule scoped to paths rides in the result of the tool call that
  first touched a matching file, once, until a compaction folds that result away.

  Scenario: The system message does not move between the steps of one turn
    Given an agent session in a workspace
    When the model reads a file, adds a todo item, then answers
    Then every request of that turn carries the same system message
    And every request repeats the previous one up to its turn context block
    And the persisted transcript carries no turn context block

  Scenario: The wall clock travels after the history
    Given an agent session in a workspace
    When the model answers straight away
    Then no request carries a wall clock reading in its system message
    And the turn context block of the request carries the current UTC time

  Scenario: The todo checklist travels after the history
    Given an agent session in a workspace
    When the model reads a file, adds a todo item, then answers
    Then no request carries the todo checklist in its system message
    And the turn context block of the last request carries the new todo item

  Scenario: A rule a tool call activated rides in the result of that call
    Given an agent session in a workspace holding a rule scoped to Go files
    When the model reads a Go file, then answers
    Then the result of the read carries the scoped rule
    And no turn context block carries the scoped rule
    And the request after the read carries the system message the turn started with

  Scenario: A later turn keeps the system message and is not told the rule again
    Given an agent session in a workspace holding a rule scoped to Go files
    When the model reads a Go file, then answers
    And the user asks again, and the model reads the same Go file, then answers
    Then every request of both turns opens with the same system message
    And only the first read's result carries the scoped rule

  Scenario: The rule comes back with the next matching read after a compaction
    Given an agent session in a workspace holding a rule scoped to Go files
    When the model reads a Go file, then answers
    And the operator compacts the conversation
    And the user asks again, and the model reads the same Go file, then answers
    Then the second read's result carries the scoped rule again
    And every request of both turns opens with the same system message

  Scenario: Editing AGENTS.md during the session does not move the system message
    Given an agent session in a workspace whose AGENTS.md says "AGENTS_FIRST_TEXT"
    When the model answers straight away
    And the workspace AGENTS.md is rewritten to say "AGENTS_SECOND_TEXT"
    And the user asks again, and the model answers straight away
    Then every request of both turns opens with the same system message
    And that system message carries "AGENTS_FIRST_TEXT"
