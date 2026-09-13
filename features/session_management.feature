Feature: Reading and pruning the stored sessions in bulk
  A history that has grown for months is managed as a table, not one row at a time:
  every stored bundle reports what it cost and which model answered it, and a single
  request removes the sessions the operator ticked, the whole history, or everything
  except the conversation that is currently open.

  Background:
    Given a running coddy HTTP server

  Scenario: Every row carries the statistics of its bundle
    Given 3 stored sessions
    And session 1 was answered by model "openai/gpt-4o" over 2 turns
    And session 1 spent 120 input and 45 output tokens
    When I list sessions with statistics
    Then session 1 reports model "openai/gpt-4o"
    And session 1 reports 4 messages
    And session 1 reports 120 input, 45 output and 165 total tokens
    And every listed session reports when it was created

  Scenario: Deleting the sessions the operator ticked
    Given 3 stored sessions
    When I delete sessions 1 and 3 in one request
    Then the response reports 2 deleted sessions
    And only session 2 is left

  Scenario: Emptying the history in one request
    Given 3 stored sessions
    When I delete every session in one request
    Then the response reports 3 deleted sessions
    And no session is left

  Scenario: Keeping the conversation that is open
    Given 3 stored sessions
    When I delete every session except session 2
    Then the response reports 2 deleted sessions
    And only session 2 is left
