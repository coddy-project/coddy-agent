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

  Scenario: A ring is walked once and quietly
    Given a ring of three relays with an agent behind the third holding a session "sess_ring" titled "ring work"
    When I list swarm sessions with the client token
    Then the list holds 1 sessions
    And the session "sess_ring" is reachable through the path "shortcut/agent7"
    And no warning is raised, because a closed ring is the shape and not a fault

  Scenario: An agent reached two equal ways is listed by the same route every time
    Given two child relays "left" and "right" that both reach the agent "x" holding a session "sess_both" titled "reached two ways"
    When I list swarm sessions with the client token 16 times
    Then every list holds the session "sess_both" once, reachable through the path "left/x"
    When I read the swarm topology with the client token
    Then the route to "x" is "left/x"

  Scenario: A node that serves only shared models raises no sessions warning
    Given the node "nas02" holds a session "sess_alpha" titled "refactor the parser"
    And the node "vault" serves only shared models and says so
    When I list swarm sessions with the client token
    Then the list holds 1 sessions
    And no warning names the node "vault"

  Scenario: A node that serves only shared models and does not say so keeps its warning
    Given the node "nas02" holds a session "sess_alpha" titled "refactor the parser"
    And the node "vault" serves only shared models without saying so
    When I list swarm sessions with the client token
    Then the list holds 1 sessions
    And a warning names the node "vault"

  Scenario: A node that says it serves only shared models but holds sessions is still listed
    Given the node "vault" says it serves only shared models but answers its sessions
    When I list swarm sessions with the client token
    Then the list holds 1 sessions
    And the session "sess_open" is labelled with the node "vault"

  Scenario: The label silences a refusal and nothing else
    Given the node "vault" serves only shared models and says so but fails with a server error
    When I list swarm sessions with the client token
    Then a warning names the node "vault"

  Scenario: The topology tells which nodes serve only shared models
    Given the node "vault" serves only shared models and says so
    And the node "nas02" holds a session "sess_alpha" titled "refactor the parser"
    When I read the swarm topology with the client token
    Then the topology marks the node "vault" as serving only shared models
    And the topology does not mark the node "nas02" as serving only shared models

