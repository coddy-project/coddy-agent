Feature: Terminal sign-in to NeuralDeep works on a machine with no browser
  `coddy providers login neuraldeep` runs as often on a server reached over SSH
  as on a laptop. The loopback callback only completes when a browser can reach
  this process's own 127.0.0.1, which a remote machine cannot, and the failure
  is silent: a page that never comes back. So the terminal login uses the device
  flow - the hub prints a short code, the person confirms it in whatever browser
  they have, and the key arrives by polling. `--browser` still asks for the
  callback when the browser really is on this machine.

  @cli
  Scenario: The terminal login uses the device flow by default
    Given a stand-in NeuralDeep hub that serves both sign-in flows
    And this machine has no local browser
    When I run the terminal sign-in to NeuralDeep
    Then the sign-in prints the verification page and the code to confirm
    And no browser was opened
    And the stored key is the one the device flow issued
    And the config gains the neuraldeep provider and its tier models

  @cli
  Scenario: A desktop is offered its browser
    Given a stand-in NeuralDeep hub that serves both sign-in flows
    And this machine has a local browser
    When I run the terminal sign-in to NeuralDeep
    Then the verification page was opened in the browser

  @cli
  Scenario: --browser keeps the loopback callback
    Given a stand-in NeuralDeep hub that serves both sign-in flows
    And this machine has a local browser
    When I run the terminal sign-in to NeuralDeep with --browser
    Then the stored key is the one the browser callback issued
