Feature: Coddy waits for a hit usage limit to lift when asked to
  Claude Desktop's "auto-continue when limits reset". A provider that names
  the moment its limit reopens (a Retry-After far beyond the retry budget)
  ends the turn with the error by default; with agent.wait_for_limit_reset
  the top-level turn waits under the turn context, keeps the countdown on
  every surface with a provider_usage update, and then re-issues the same
  call. The user message is persisted before the call, so nothing repeats.

  Scenario: With the option on, the turn waits for the reset and then answers
    Given an agent whose provider first reports a limit that lifts in 1 s and then answers "done"
    And wait_for_limit_reset is on
    When the user sends a turn
    Then the turn ends with "done" after 2 provider calls
    And the turn took at least 1 s
    And the client saw a resuming usage update with the reset time

  Scenario: Off by default, the turn ends with the provider error
    Given an agent whose provider first reports a limit that lifts in 1 s and then answers "done"
    When the user sends a turn
    Then the turn fails with the quota reset error after 1 provider call
    And the client saw no resuming usage update

  Scenario: A pause beyond the configured maximum ends the turn at once
    Given an agent whose provider first reports a limit that lifts in 1 s and then answers "done"
    And wait_for_limit_reset is on with a maximum of 300 ms
    When the user sends a turn
    Then the turn fails with the quota reset error after 1 provider call
    And the turn took less than 300 ms

  Scenario: The retry wrapper's own verdict drives the wait
    Given an agent whose provider answers a 429 naming a reset in 1 s through the retry wrapper and then answers "done"
    And wait_for_limit_reset is on
    When the user sends a turn
    Then the turn ends with "done" after 2 provider calls
    And the turn took at least 1 s
    And the client saw a resuming usage update with the reset time

  Scenario: A second limit in the same turn counts against the same maximum
    Given an agent whose provider reports a limit that lifts in 1 s twice and then answers "done"
    And wait_for_limit_reset is on with a maximum of 1500 ms
    When the user sends a turn
    Then the turn fails with the quota reset error after 2 provider calls
    And the turn took at least 1 s

  Scenario: The maximum bounds the retry wrapper's own sleeps as well
    Given an agent whose provider answers a 429 naming a reset in 1 s through the retry wrapper and then answers "done"
    And wait_for_limit_reset is on with a maximum of 300 ms
    When the user sends a turn
    Then the turn fails with the quota reset error after 1 provider call
    And the turn took less than 300 ms

  Scenario: The retry wrapper's own sleeps count against the same maximum
    Given an agent whose provider answers a 429 naming a reset in 1 s twice through the retry wrapper and then answers "done"
    And wait_for_limit_reset is on with a maximum of 1500 ms
    When the user sends a turn
    Then the turn fails with the quota reset error after 2 provider calls
    And the turn took at least 1 s
    And the turn took less than 1500 ms

  Scenario: An explicit zero never sleeps on a limit, the retry wrapper included
    Given an agent whose provider answers a 429 naming a reset in 1 s through the retry wrapper and then answers "done"
    And wait_for_limit_reset is on with a maximum of 0 ms
    When the user sends a turn
    Then the turn fails with the quota reset error after 1 provider call
    And the turn took less than 300 ms
