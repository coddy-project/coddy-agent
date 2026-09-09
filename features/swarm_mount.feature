Feature: Driving one node through a relay
  A relay carries a client's request to the node it names and carries the
  answer back, streaming included. The client authenticates to the relay; the
  relay authenticates to the node. Neither credential crosses into the other's
  territory.

  Background:
    Given a swarm relay with pairing token "pair-secret" and client token "client-secret"
    And an agent node "nas02" that reports what it receives

  Scenario: A request reaches the node through its mount
    When I call "/coddy/sessions" on node "nas02" with the client token
    Then the node received the path "/coddy/sessions"
    And the response comes from the node

  Scenario: The relay presents the node's own credential, not the client's
    When I call "/coddy/sessions" on node "nas02" with the client token
    Then the node saw the authorization "Bearer node-secret"

  Scenario: A streamed answer arrives chunk by chunk
    When I stream "/v1/responses" from node "nas02" with the client token
    Then I receive the streamed chunks as they are produced

  Scenario: A client cannot register a node through a mount
    When I call "/swarm/register" on node "nas02" with the client token
    Then the request is refused as not carried

  Scenario: An unknown node is named in the error
    When I call "/coddy/sessions" on node "ghost" with the client token
    Then the error names the node "ghost"

  Scenario: A mount still needs the client token
    When I call "/coddy/sessions" on node "nas02" without any credential
    Then the request is rejected as unauthorized
