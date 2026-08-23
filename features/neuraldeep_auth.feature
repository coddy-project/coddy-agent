Feature: NeuralDeep hub sign-in feeds the neuraldeep provider
  A "neuraldeep" provider normally takes a hand-pasted api_key. Signing in
  through the NeuralDeep hub replaces that chore: the browser flow hands a
  per-user key to a loopback callback (CLI), or the device flow hands it to a
  polling client (SPA settings and headless machines). Either way the key is
  stored in the provider's private auth file, requests authenticate with it,
  and the tier's model catalog becomes visible without editing YAML by hand.

  @cli
  Scenario: Browser sign-in stores the hub key and unlocks the tier models
    Given a stand-in NeuralDeep hub and API for provider "neuraldeep"
    When I sign in to NeuralDeep with the browser callback flow
    Then the neuraldeep auth file holds the hub key with private permissions
    And the provider model list is fetched with the hub key
    And the config gains the neuraldeep provider and its tier models

  @http
  Scenario: HTTP device sign-in connects the provider for the SPA
    Given a coddy HTTP server with a neuraldeep provider and a stand-in hub
    When I sign in to NeuralDeep through the device flow over REST
    Then the neuraldeep provider reports connected with a masked key
    And the provider model list is fetched with the hub key
    When I sign out of NeuralDeep over REST
    Then the neuraldeep provider reports disconnected
