Feature: The usage of a whole swarm, read through its relay
  A relay merges what its nodes recorded into one report, each node counted
  once however many routes reach it. A node is also readable on its own
  through its mount, which is how a browser starts an import on it. The nodes
  here are real coddy servers with real statistics stores, and the relay is a
  real relay.

  Background:
    Given a relay with the coddy nodes "nas02" and "gpu03"

  Scenario: What two nodes recorded arrives as one report
    Given the node "nas02" recorded 120 tokens of "openai/gpt-5" today
    And the node "gpu03" recorded 300 tokens of "anthropic/claude" today
    When I read the swarm statistics with the client token
    Then the report holds 420 tokens from 2 nodes
    And the models of the report are "anthropic/claude" and "openai/gpt-5"

  Scenario: A node two routes reach is counted once
    Given the node "nas02" recorded 120 tokens of "openai/gpt-5" today
    And the node "nas02" is registered a second time as "nas02-backup"
    When I read the swarm statistics with the client token
    Then the report holds 120 tokens from 2 nodes
    And the node "nas02" is listed once

  Scenario: A filter reaches the nodes themselves
    Given the node "nas02" recorded 120 tokens of "openai/gpt-5" today
    And the node "gpu03" recorded 300 tokens of "anthropic/claude" today
    When I read the swarm statistics filtered by the model "openai/gpt-5"
    Then the report holds 120 tokens from 2 nodes
    And each node was asked for its own summary, never for its whole history

  Scenario: The relay decides which day a node's work falls on
    A node keeps a time zone of its own for its own screen. A fleet report has
    to lay every node out on one calendar, or the days of two machines cannot
    be added at all, so the zone the relay asks for wins over each node's.

    Given the node "nas02" keeps its own time zone "Australia/Sydney"
    And the node "nas02" recorded 120 tokens of "openai/gpt-5" at 22:00 UTC yesterday
    When I read the swarm statistics with the client token
    Then the report is laid out in "UTC"
    And the one day of the report is yesterday in "UTC"

  Scenario: One node is read on its own through its mount
    Given the node "nas02" recorded 120 tokens of "openai/gpt-5" today
    And the node "gpu03" recorded 300 tokens of "anthropic/claude" today
    When I read the statistics of the node "nas02" through its mount
    Then that answer holds 120 tokens

  Scenario: A node that is gone becomes a warning beside the results
    Given the node "nas02" recorded 120 tokens of "openai/gpt-5" today
    And a node "gone" is registered but unreachable
    When I read the swarm statistics with the client token
    Then the report holds 120 tokens from 2 nodes
    And a warning names the node "gone"
