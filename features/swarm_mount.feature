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

  Scenario: A page of a long transcript reaches the node with its window
    When I call "/coddy/sessions/sess_long/messages?limit=60&before=3200" on node "nas02" with the client token
    Then the node received the path "/coddy/sessions/sess_long/messages"
    And the node received the query "before=3200&limit=60"

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

  Scenario: A browser hears the relay's CORS answer once, whatever the node says
    Given the relay allows the browser origin "https://app.example"
    And the node answers with CORS headers of its own
    When a browser at "https://app.example" calls "/coddy/sessions" on node "nas02" with the client token
    Then the response comes from the node
    And the response allows the origin "https://app.example" exactly once
