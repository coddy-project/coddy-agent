Feature: A laptop's page on any loopback port may call a remote coddy serve
  An operator drives a coddy serve on a server from a laptop that has no fixed
  address. The web UI is served by the laptop's own coddy serve, so the browser
  calls the server from http://localhost:<port>, http://127.0.0.1:<port> or
  http://[::1]:<port>, and the port moves between installations.
  httpserver.cors.allow_loopback admits every such origin at once, where
  allowed_origins would need the exact string of each. The bearer token still
  applies to every real request.

  Scenario: A preflight from a loopback page is answered with its origin
    Given a coddy HTTP server with token "remote-secret" whose CORS allows loopback origins
    When a page at "http://localhost:5173" sends a preflight for GET "/v1/models"
    Then the preflight is answered with 204
    And the response allows the origin "http://localhost:5173"

  Scenario: A loopback page on any port and in any spelling reads the model list
    Given a coddy HTTP server with token "remote-secret" whose CORS allows loopback origins
    When a page at "http://127.0.0.1:12345" requests "/v1/models" with the token
    Then the request succeeds
    And the response allows the origin "http://127.0.0.1:12345" and varies by origin
    When a page at "http://[::1]:12345" requests "/v1/models" with the token
    Then the request succeeds
    And the response allows the origin "http://[::1]:12345" and varies by origin

  Scenario: The setting applies to a running server without a restart
    Given a coddy HTTP server with token "remote-secret" whose CORS lists only "http://ui.local"
    When a page at "http://localhost:5173" requests "/v1/models" with the token
    Then the response allows no origin
    When the running server's configuration is replaced with CORS allowing loopback origins
    And a page at "http://localhost:5173" requests "/v1/models" with the token
    Then the response allows the origin "http://localhost:5173"
