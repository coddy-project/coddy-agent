Feature: A client certificate is a credential for shared models on a Coddy's own listener
  A coddy serve that terminates TLS itself can ask for client certificates
  (httpserver.tls.client_ca_file). The names of a verified certificate that
  httpserver.shared_models.cert_names lists open the three shared-model routes
  and nothing else, and each name has its own stream and rate budget, so two
  borrowers are told apart without a relay in front.

  Scenario: A listed certificate lists and calls the shared models with no token
    Given a remote coddy over TLS that admits the certificate names "alice.example" and "bob.example"
    When a client with a certificate for "alice.example" lists the shared models
    And the same client calls the shared model "coder"
    Then both requests succeed
    And the remote counted one call of "coder" in the class "mtls"

  Scenario: The certificate opens no other route
    Given a remote coddy over TLS that admits the certificate names "alice.example" and "bob.example"
    When a client with a certificate for "alice.example" reads the sessions of the remote
    Then the remote answers 401

  Scenario: Each certificate name has a budget of its own
    Given a remote coddy over TLS that admits the certificate names "alice.example" and "bob.example", one call a minute each
    When a client with a certificate for "alice.example" calls the shared model "coder" twice
    And a client with a certificate for "bob.example" calls the shared model "coder"
    Then the second call of "alice.example" is refused as busy with the code "rate_window"
    And the call of "bob.example" succeeds
