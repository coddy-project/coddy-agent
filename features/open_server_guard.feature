Feature: A coddy serve without a credential answers only its own machine's clients
  coddy serve starts on 127.0.0.1 with no token and no sign-in, so anything that
  can reach the port can use the API. Programs on the same machine reach it by a
  loopback name, and the web UI it serves talks to it from its own origin. A page
  of another site open in the same browser is neither: it either names the
  server by a host name of its own or sends a request that carries its own
  origin. While no credential is configured and the request arrived on loopback,
  the server answers the first with 421 and refuses a state-changing request of
  the second with 403. A credential leaves the decision to the auth gate.

  Scenario: Local clients and the server's own page keep working
    Given a coddy HTTP server on loopback without a credential
    When a client without an origin posts to "/v1/chat/completions"
    Then the request passes the guard
    When a page of the server's own origin posts to "/v1/chat/completions"
    Then the request passes the guard
    When a client asks "localhost" for "/v1/models"
    Then the request passes the guard

  Scenario: A page of another site cannot change state
    Given a coddy HTTP server on loopback without a credential
    When a page at "https://example.com" posts to "/v1/chat/completions"
    Then the request is refused with 403

  Scenario: The server does not answer a host name that is not loopback
    Given a coddy HTTP server on loopback without a credential
    When a client asks "example.com" for "/v1/models"
    Then the request is refused with 421

  Scenario: A configured token leaves the decision to the auth gate
    Given a coddy HTTP server on loopback with token "secret"
    When a client asks "example.com" for "/v1/models"
    Then the request is refused with 401
