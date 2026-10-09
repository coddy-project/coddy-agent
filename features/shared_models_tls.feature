Feature: TLS admits a peer at the handshake and gives it no rights
  A coddy serve that terminates TLS itself can require a client certificate
  (httpserver.tls.client_ca_file): the handshake admits a peer whose certificate
  chains to that authority and refuses one that does not. Coddy reads no identity
  out of the certificate, so a certificate is no credential and no budget: the
  credential is a token, and the budget is the token's. What a certificate holder
  may do is for the reverse proxy and the infrastructure.

  Scenario: A certificate alone opens nothing
    Given a remote coddy over TLS that requires a client certificate
    When a client with a certificate reads the shared models with no token
    Then the remote answers 401

  Scenario: A token over a certificate lists and calls the shared models
    Given a remote coddy over TLS that requires a client certificate
    When a client with a certificate lists the shared models with the shared token
    And the same client calls the shared model "coder" with the shared token
    Then both requests succeed
    And the remote counted one call of "coder" in the class "shared"

  Scenario: Two certificates carrying one token share one budget
    Given a remote coddy over TLS that requires a client certificate, one call a minute
    When a client with a first certificate calls the shared model "coder" with the shared token
    And a client with a second certificate calls the shared model "coder" with the shared token
    Then the second call is refused as busy with the code "rate_window"

  Scenario: A peer without a certificate is refused at the handshake
    Given a remote coddy over TLS that requires a client certificate
    When a client with no certificate connects
    Then the handshake fails
