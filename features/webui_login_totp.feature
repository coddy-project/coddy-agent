Feature: One-time codes at the web UI sign-in
  An operator who wants more than a password on a server reachable from a
  network turns on one-time codes with `coddy serve set-totp`: the command
  shows a QR code, an authenticator app (Google Authenticator, FreeOTP, Aegis,
  a password manager) adds the account under the name "Coddy", and from then on
  the sign-in form asks for the six digits the app shows next to the password.
  The bearer token API clients present is a credential of its own and is not
  asked for a code.

  Background:
    Given a coddy HTTP server with the account "operator", password "correct-horse" and one-time codes

  Scenario: The sign-in state tells the form to ask for a code
    When the browser asks whether sign-in is required
    Then the request succeeds
    And the answer says sign-in is required
    And the answer says a one-time code is required

  Scenario: The password and the current code open the API
    When the browser signs in as "operator" with password "correct-horse" and the current code
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
    When the browser signs in as "operator" with password "correct-horse" and the current code
    Then the request succeeds

  Scenario: A bearer client is not asked for a code
    Given the server also requires the bearer token "relay-secret"
    When a client presents the bearer token "relay-secret"
    And the client requests the model list
    Then the request succeeds
