Feature: Every session in the swarm, in one list
  A relay merges the session lists of the nodes it knows, labelling each row
  with the node that owns it. A node that is slow or gone becomes a warning
  beside the results rather than an error instead of them.

  Background:
    Given a swarm relay with pairing token "pair-secret" and client token "client-secret"

  Scenario: Sessions from two nodes arrive in one list
    Given the node "nas02" holds a session "sess_alpha" titled "refactor the parser"
    And the node "gpu03" holds a session "sess_beta" titled "train the model"
    When I list swarm sessions with the client token
    Then the list holds 2 sessions
    And the session "sess_alpha" is labelled with the node "nas02"
    And the session "sess_beta" is labelled with the node "gpu03"

  Scenario: The same session id on two nodes stays two sessions
    Given the node "nas02" holds a session "sess_same" titled "on nas02"
    And the node "gpu03" holds a session "sess_same" titled "on gpu03"
    When I list swarm sessions with the client token
    Then the list holds 2 sessions
    And both rows are told apart by their node

  Scenario: Searching by what the work is about
    Given the node "nas02" holds a session "sess_alpha" titled "refactor the parser"
    And the node "gpu03" holds a session "sess_beta" titled "train the model"
    When I search swarm sessions for "parser"
    Then the list holds 1 sessions
    And the session "sess_alpha" is labelled with the node "nas02"

  Scenario: Searching by the node itself returns everything it holds
    Given the node "nas02" holds a session "sess_alpha" titled "refactor the parser"
    And the node "gpu03" holds a session "sess_beta" titled "train the model"
    When I search swarm sessions for "gpu03"
    Then the list holds 1 sessions
    And the session "sess_beta" is labelled with the node "gpu03"

  Scenario: A node that is gone becomes a warning, not a failure
    Given the node "nas02" holds a session "sess_alpha" titled "refactor the parser"
    And the node "gone" is registered but unreachable
    When I list swarm sessions with the client token
    Then the list holds 1 sessions
    And a warning names the node "gone"

  Scenario: Sessions behind a second relay arrive through it
    Given a child relay "inner" holding a node "agent7" with a session "sess_deep" titled "deep work"
    When I list swarm sessions with the client token
    Then the session "sess_deep" is reachable through the path "inner/agent7"

  Scenario: A chain that loops back on itself stops without crying wolf
    Given a child relay "inner" holding a node "agent7" with a session "sess_deep" titled "deep work"
    When the child relay asks this relay for its sessions as part of the same chain
    Then the answer is empty and says the branch was already walked
    And no warning is raised, because a closed ring is the shape and not a fault
