Feature: Scoped clients of a swarm relay
  A relay client can hold a token of its own that opens only the shared-model
  routes of the nodes its entry lists. The full client token keeps opening the
  whole relay; a scoped client never reaches the relay's own routes, a node it
  was not given, or a route outside the shared-model ones (the three calls and the probe's ping).

  Background:
    Given a swarm relay with the full token "full-secret" and a scoped client "acme" with the token "acme-secret" that may reach the node "nas02"
    And the node "nas02" answers every route it is asked

  Scenario: A scoped client lists the shared models and calls one through the relay
    When the scoped client lists the models of the node "nas02"
    And the scoped client calls the completions of the node "nas02"
    Then both requests reached the node
    And the node saw its own credential and never the client's

  Scenario: A scoped client reaches a node that dialled out through its tunnel
    Given the node "inner" is online through a tunnel and the scoped client may reach it
    When the scoped client lists the models of the node "inner"
    Then the request reached the node "inner"

  Scenario: A scoped client is refused on the relay's own routes
    When the scoped client reads the relay's node list
    Then the relay answers 401 as it would to an unknown token

  Scenario: A node outside the client's list is not told apart from a node that does not exist
    Given the node "other" is registered but not in the client's list
    When the scoped client lists the models of the node "other"
    And the scoped client lists the models of the node "ghost"
    Then both answers are the same, up to the node's name
    And the node "other" was never reached

  Scenario: A route outside the three shared-model routes is refused
    When the scoped client reads the sessions of the node "nas02"
    Then the request is refused as not carried
    And no request reached the node

  Scenario: The full token keeps opening the whole relay
    When the full client reads the relay's node list
    Then the relay answers 200

  Scenario: A client at its slot limit is told so and the node is not asked
    Given a relay whose client "acme" may hold 1 shared-model call at a time, and a node that holds every call open
    When the client "acme" starts a shared-model call
    And the client "acme" starts a second shared-model call
    Then the second call is refused with 429 busy and the code "client_streams"
    And the node saw only the first call

  Scenario: A client past its window is told when to come back
    Given a relay whose client "acme" may start 60 shared-model calls a minute with a burst of 1, and a node that answers at once
    When the client "acme" starts a shared-model call
    And the client "acme" starts a second shared-model call
    Then the second call is refused with 429 busy and the code "client_rate"
    And the retry hint of the refusal is 1 second
    And the node saw only the first call

  Scenario: The counters of the relay tell what happened, to the full class only
    Given a relay whose client "acme" may start 60 shared-model calls a minute with a burst of 1, and a node that answers at once
    When the client "acme" starts a shared-model call
    And the client "acme" starts a second shared-model call
    Then the relay counted 1 "ok" and 1 "limit" call for the client "acme"
    And the scoped client "acme" cannot read the counters
