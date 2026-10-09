Feature: Coddy makes the TLS certificates it needs
  TLS is the transport's business: Coddy creates its certificates with its own
  means at installation, at update and on demand (coddy tls), and reads no
  identity out of them. What a certificate holder may do is for the reverse proxy.

  Scenario: ensure makes a CA, a server pair, a client pair and a bundle, and a second run does nothing
    Given a machine "a" with no certificates
    When the operator runs "coddy tls ensure" on "a"
    Then machine "a" has a CA, a server pair, a client pair and a bundle
    And the private keys of machine "a" are readable by its owner only
    When the operator runs "coddy tls ensure" on "a"
    Then the output says there was nothing to do

  Scenario: two machines trust each other by exchanging CAs and never a private key
    Given a machine "a" with certificates
    And a machine "b" with certificates
    When machine "a" serves HTTPS and asks for client certificates
    Then machine "b" cannot call it
    When machine "b" trusts the CA of machine "a"
    And machine "a" trusts the CA of machine "b"
    And machine "a" serves HTTPS and asks for client certificates
    Then machine "b" can call it
    And no private key of either machine was exchanged

  Scenario: a client certificate for something that has no Coddy
    Given a machine "a" with certificates
    When the operator issues a client certificate named "proxy" on "a"
    Then the issued certificate verifies against the CA that was written next to it
