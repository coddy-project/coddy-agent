Feature: Swarm relay registry
  A swarm relay is a stateless meeting point. Nodes register into it, refresh
  that registration, and are listed for anyone allowed to look. The relay keeps
  the list only in memory, so the nodes themselves are what make it true.

  Background:
    Given a swarm relay with pairing token "pair-secret" and client token "client-secret"

  Scenario: A relay announces itself before anyone holds a credential
    When I read the relay info without any credential
    Then the response says it is a swarm
    And the response carries a relay uuid

  Scenario: A node registers and is listed
    When the node "nas02" registers with the pairing token
    Then the registration is accepted
    And the registration returns a lease secret
    When I list the relay nodes with the client token
    Then the node "nas02" is listed as online

  Scenario: Registration needs the pairing token
    When the node "nas02" registers with the pairing token "wrong-secret"
    Then the registration is rejected as unauthorized

  Scenario: The node list needs the client token
    When I list the relay nodes without any credential
    Then the request is rejected as unauthorized

  Scenario: A node refreshes its own registration
    Given the node "nas02" has registered with the pairing token
    When the node "nas02" registers again with its lease secret
    Then the registration is accepted
    And the lease generation has moved

  Scenario: A second node cannot claim a name that is already live
    Given the node "nas02" has registered with the pairing token
    When the node "nas02" registers again without a lease secret
    Then the registration is rejected as a conflict

  Scenario: An operator removes a node
    Given the node "nas02" has registered with the pairing token
    When I remove the node "nas02" with the client token
    Then the node "nas02" is no longer listed
