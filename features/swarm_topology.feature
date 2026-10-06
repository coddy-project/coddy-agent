Feature: The shape of the swarm
  Relays are not required to form a tree. A client attached to one asks it what
  the swarm looks like, and gets back the nodes, how they connect, and the
  shortest way to reach each of them.

  Background:
    Given a swarm relay with pairing token "pair-secret" and client token "client-secret"

  Scenario: A relay describes itself and its nodes
    Given the node "nas02" holds a session "sess_alpha" titled "refactor the parser"
    When I read the swarm topology with the client token
    Then the topology names this relay as its root
    And the topology holds the node "nas02"
    And the topology has a route to "nas02"

  Scenario: Nodes behind a second relay appear with a route through it
    Given a child relay "inner" holding a node "agent7" with a session "sess_deep" titled "deep work"
    When I read the swarm topology with the client token
    Then the topology holds the node "agent7"
    And the route to "agent7" is "inner/agent7"

  Scenario: A ring says where it closes and raises no warning
    Given a ring of three relays with an agent behind the third holding a session "sess_ring" titled "ring work"
    When I read the swarm topology with the client token
    Then the topology lists 4 nodes, each of them once
    And an edge leads from relay3 back to this relay
    And the topology raises no warning
    And the route to "agent7" is "shortcut/agent7"
    And the route to "relay3" is "shortcut", with "middle/relay3" kept as an alternate
