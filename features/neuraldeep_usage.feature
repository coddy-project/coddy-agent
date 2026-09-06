Feature: NeuralDeep account usage on every surface
  A neuraldeep provider meters the account by subscription windows (a 3-hour
  session and a week) and, for wallet keys, by a ruble balance. The hub
  exposes the live remainders through a read-only endpoint, and Coddy reads it
  so the operator sees the limit coming instead of learning about it from a
  429 in the middle of a turn. The manager owns one cache and one schedule, so
  the console footer, the remote console, the SPA and scripts all read the
  same numbers.

  @http
  Scenario: The account usage of a NeuralDeep provider is readable over REST
    Given a coddy HTTP server with a neuraldeep provider, a stored hub login and a stand-in limits API
    When I read the neuraldeep provider usage over REST
    Then the usage names the plan "pro" with the session window at 3% and the week window at 7%
    And the usage carries the reset times and the wallet balance of the account
    And the stand-in limits API was asked with the hub key and the answer never carries it

  @http
  Scenario: A finished turn refreshes the account usage
    Given a coddy HTTP server with a neuraldeep provider, a stored hub login and a stand-in limits API
    And the stand-in limits API will report the session window at 42% after the next turn
    When a prompt turn finishes on the server
    Then the server-wide events stream announces the neuraldeep usage at 42%
    And the neuraldeep provider usage over REST shows the session window at 42%

  @http
  Scenario: A provider without a usage source says so
    Given a coddy HTTP server with a neuraldeep provider, a stored hub login and a stand-in limits API
    When I read the usage of the "stub" provider over REST
    Then the usage answer marks the provider as unsupported
