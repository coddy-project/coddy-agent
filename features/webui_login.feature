Feature: Web UI sign-in
  An operator who puts `coddy serve` on a network closes it with an account
  instead of leaving every session readable. A browser signs in once at a form,
  carries an HttpOnly cookie afterwards, and signs out server-side. The bearer
  token API clients already use is untouched: the two credentials open the same
  gate, so the console, an editor and a swarm relay keep reaching the node they
  always reached.

  Background:
    Given a coddy HTTP server with the account "operator" and password "correct-horse"

  Scenario: An anonymous browser reads nothing
    When the browser requests the model list
    Then the request is refused with 401
    When the browser requests the session list
    Then the request is refused with 401
    When the browser requests the server config
    Then the request is refused with 401

  Scenario: The page itself stays public so a browser can reach the form
    When the browser requests the application shell
    Then the request is not refused by the gate

  Scenario: The sign-in state is public, so the app knows what to render
    When the browser asks whether sign-in is required
    Then the request succeeds
    And the answer says sign-in is required
    And the answer says the browser is not signed in

  Scenario: Signing in opens the API to that browser
    When the browser signs in as "operator" with password "correct-horse"
    Then the request succeeds
    And the browser holds a session cookie
    When the browser requests the model list
    Then the request succeeds
    And the model list includes profiles "agent, plan"
    When the browser asks whether sign-in is required
    Then the answer says the browser is signed in as "operator"

  Scenario: A signed-in browser reads the event stream without a token in the URL
    Given the browser is signed in as "operator" with password "correct-horse"
    When the browser opens the server event stream
    Then the request succeeds

  Scenario: A signed-in browser may change state
    Given the browser is signed in as "operator" with password "correct-horse"
    And a session rooted at the workspace
    When the browser moves that session from its own origin
    Then the request succeeds

  Scenario: Signing out ends the session on the server
    Given the browser is signed in as "operator" with password "correct-horse"
    When the browser signs out
    Then the request succeeds
    When the browser requests the model list
    Then the request is refused with 401

  Scenario: A bearer client keeps working while the form is on
    Given the server also requires the bearer token "relay-secret"
    When a client presents the bearer token "relay-secret"
    And the client requests the model list
    Then the request succeeds
    And the model list includes profiles "agent, plan"

  Scenario: A bearer client never needs the cookie flow
    Given the server also requires the bearer token "relay-secret"
    And a session rooted at the workspace
    When a client presents the bearer token "relay-secret"
    And the client moves that session with no browser headers at all
    Then the request succeeds

  Scenario: The config endpoint hides the password hash and names the source
    Given the browser is signed in as "operator" with password "correct-horse"
    When the browser requests the server config
    Then the request succeeds
    And the config response hides the password hash
    And the config response reports the login source "config"

  Scenario: An account from the environment enables the form on its own
    Given a coddy HTTP server whose account comes from the environment as "envuser" with password "env-pass"
    When the browser asks whether sign-in is required
    Then the answer says sign-in is required
    When the browser signs in as "envuser" with password "env-pass"
    Then the request succeeds
    When the browser requests the model list
    Then the request succeeds
    When the browser requests the server config
    Then the config response reports the login source "env"

  Scenario: An explicit switch in the file turns the form off with the variables still set
    Given a coddy HTTP server whose account comes from the environment as "envuser" with password "env-pass"
    And the config turns the login off
    When the browser asks whether sign-in is required
    Then the answer says sign-in is not required
    When the browser requests the model list
    Then the request succeeds

  Scenario: Rotating the password ends the sessions it opened
    Given the browser is signed in as "operator" with password "correct-horse"
    When the operator changes the password to "new-horse"
    And the browser requests the model list
    Then the request is refused with 401
    When the browser signs in as "operator" with password "new-horse"
    Then the request succeeds

  Scenario: Turning the login off at runtime reopens the API
    Given the browser is signed in as "operator" with password "correct-horse"
    When the operator turns the login off
    And the browser signs out
    And the browser requests the model list
    Then the request succeeds

  Scenario: A server with no account behaves exactly as before
    Given a coddy HTTP server with no account and no token
    When the browser asks whether sign-in is required
    Then the answer says sign-in is not required
    When the browser requests the model list
    Then the request succeeds
