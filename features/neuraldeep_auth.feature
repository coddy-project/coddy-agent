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

  @http
  Scenario: The provider is pinned to the international mirror
    NeuralDeep serves the same API from two deployments: api.neuraldeep.ru for
    Russia and api.neuraldeep.tech for everywhere else. Settings picks one, and
    the choice has to survive the save.

    Given a coddy HTTP server with a neuraldeep provider and a stand-in hub
    When I point the neuraldeep provider at the international mirror over REST
    Then the saved config keeps the neuraldeep provider on the mirror

  @http
  Scenario: Signing in from Settings follows the endpoint picked in the form
    The endpoint picker and the sign-in button share one unsaved form, so the
    sign-in has to follow the pick rather than the last saved row: a key minted
    by one deployment is not honored by the other.

    Given a coddy HTTP server with a neuraldeep provider and a stand-in hub for each deployment
    When I sign in through the REST device flow with the international mirror selected
    Then the stored login was issued by the mirror hub
    And the sign-in status names the mirror hub for the mirror endpoint
