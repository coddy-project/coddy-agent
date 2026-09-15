Feature: Stable request prefix for provider prompt caching
  A provider caches a request by its prefix: the tool definitions, then the messages
  from the start. The first byte that differs from the previous request throws away
  everything cached after it, the replayed conversation included. Coddy therefore
  keeps the system message and the history byte-identical between the steps of a
  turn, and sends what moves per step - the wall clock, the todo checklist, the
  rules a tool call activated - in a turn context block appended after the history.

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

  Scenario: A rule a tool call activated reaches the model after the history
    Given an agent session in a workspace holding a rule scoped to Go files
    When the model reads a Go file, then answers
    Then the request after the read carries the scoped rule in its turn context block
    And the request after the read carries the system message the turn started with
