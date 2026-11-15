Feature: One-time codes at the web UI sign-in
  An operator can protect the web sign-in with an authenticator or a backup
  code. A password opens a short-lived challenge, not a browser session.
  A bearer token is a separate credential and does not require a code.

  Background:
    Given a coddy HTTP server with the account "operator", password "correct-horse" and one-time codes

  Scenario: The sign-in state tells the form to ask for a code
    When the browser asks whether sign-in is required
    Then the request succeeds
    And the answer says sign-in is required
    And the answer says a one-time code is required

  Scenario: The password and the current code open the API
    When the browser signs in as "operator" with password "correct-horse"
    Then the answer contains a pending challenge without a session
    When the browser verifies the current code
    Then the request succeeds
    And the browser holds a session cookie
    When the browser requests the model list
    Then the request succeeds
    When the browser asks whether sign-in is required
    Then the answer says the browser is signed in as "operator"

  Scenario: Turning one-time codes on ends the sessions the password alone opened
    Given a coddy HTTP server with the account "operator" and password "correct-horse"
    And the browser is signed in as "operator" with password "correct-horse"
    When the operator turns one-time codes on
    And the browser requests the model list
    Then the request is refused with 401
    When the browser signs in as "operator" with password "correct-horse"
    Then the answer contains a pending challenge without a session
    When the browser verifies the current code
    Then the request succeeds

  Scenario: A backup code opens the API
    Given backup codes are available
    When the browser signs in as "operator" with password "correct-horse"
    Then the answer contains a pending challenge without a session
    When the browser verifies a backup code
    Then the request succeeds
    And the browser holds a session cookie
    When the browser requests the model list
    Then the request succeeds

  Scenario: A bearer client is not asked for a code
    Given the server also requires the bearer token "relay-secret"
    When a client presents the bearer token "relay-secret"
    And the client requests the model list
    Then the request succeeds
