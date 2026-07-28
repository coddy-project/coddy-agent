Feature: Remote API parity
  A client (the Coddy UI or a script) can drive a remote, already-running,
  authenticated coddy HTTP server exactly like a local one: the bearer token
  gates the API, and sessions, workspace/cwd changes, transcripts, and
  streaming all behave the same as against a local server. Switching back to a
  local (unauthenticated) server keeps the same client working.

  Background:
    Given an authenticated coddy HTTP server with token "remote-secret"

  Scenario: Protected routes reject unauthenticated clients
    Given the client presents no token
    When I request the model list
    Then the request is rejected as unauthorized

  Scenario: Protected routes reject an invalid token
    Given the client presents an invalid token
    When I request the model list
    Then the request is rejected as unauthorized

  Scenario: A remote client with the token sees the same model list
    Given the client presents the token
    When I request the model list
    Then the request succeeds
    And the model list includes profiles "agent, plan"

  Scenario: A remote client can change the session working directory
    Given the client presents the token
    And a workspace folder "alpha"
    And a workspace folder "beta"
    And a session rooted at folder "alpha"
    When I switch the session workspace to folder "beta"
    Then the request succeeds
    And the context path points to folder "beta"
    And the session cwd is persisted as folder "beta"

  Scenario: A remote client can load a session transcript
    Given the client presents the token
    And a workspace folder "work"
    And a session rooted at folder "work"
    When I send the prompt "hello remote" to the session
    Then the request succeeds
    And the streamed response terminates cleanly
    And the session transcript includes the prompt "hello remote"

  Scenario: A remote client can list persisted sessions
    Given the client presents the token
    And a workspace folder "work"
    And a session rooted at folder "work"
    When I list sessions
    Then the request succeeds
    And the session list includes the session

  Scenario: The config endpoint never returns the auth token to a remote client
    Given the client presents the token
    When I request the server config
    Then the request succeeds
    And the config response hides the auth token
    And the config response reports authentication is configured

  Scenario: Switching back to a local server keeps the client working without a token
    Given a local coddy HTTP server without authentication
    And the client presents no token
    When I request the model list
    Then the request succeeds
    And the model list includes profiles "agent, plan"
