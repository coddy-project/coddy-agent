Feature: A running turn reports its progress
  A long turn has to show that it is alive: how long it has been running and
  how much the model has written so far. The agent is the one place that sees
  every delta and every provider usage figure, so it publishes one update,
  turn_progress, that every surface renders the same way - the web UI, the
  console, a console attached to a remote server. Before the first token the
  update carries the clock alone, so a surface reads "waiting for the model"
  with a running time and no token count.

  Scenario: The turn announces its clock before the model has answered
    Given an agent whose model stays silent for 300 ms and then answers "done" reporting 40 output tokens
    When the user sends a turn
    Then the first progress update carries the turn start and no tokens
    And that update was sent before the first text of the answer

  Scenario: The count follows the stream and settles on what the provider reported
    Given an agent whose model streams 2400 characters over 1500 ms and reports 500 output tokens
    When the user sends a turn
    Then a progress update sent while the answer streamed carries an estimated token count above zero
    And the last progress update carries 500 tokens and is not an estimate

  Scenario: The count adds up across the calls of one turn
    Given an agent whose model calls a tool reporting 120 output tokens and then answers reporting 80 output tokens
    When the user sends a turn
    Then the last progress update carries 200 tokens and is not an estimate
    And every progress update carries the same turn start

  Scenario: A provider that reports no usage leaves the estimate standing
    Given an agent whose model answers 400 characters and reports no usage
    When the user sends a turn
    Then the last progress update carries 100 tokens and is an estimate

  Scenario: The session keeps the progress for a client that joins late
    Given an agent whose model streams 2400 characters over 1500 ms and reports 500 output tokens
    When the user sends a turn
    Then the session state holds the turn start and 500 output tokens
