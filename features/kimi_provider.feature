Feature: Kimi Code credentials serve Coddy's agent
  A Kimi Code subscription supplies the model and usage data to Coddy. Its
  device authorization, credential storage, model catalog and inference use
  the selected region. The stand uses synthetic credentials and no network.

  Scenario: A managed global subscription signs in and answers a model request
    Given a Kimi subscription stand in the global region
    When the operator completes the Kimi device authorization
    Then Coddy stores its own credential for the global region
    And the subscription model catalog uses that credential
    And the subscription usage contains its independent limits
    When Coddy sends a model request with its stored credential
    Then the model answers with the stand's response

  Scenario: Every subscription request follows the configured provider proxy
    Given a Kimi provider configured with a local proxy
    When Coddy starts authorization, polls the device and requests refresh, discovery, usage and inference
    Then each request reaches the configured proxy

  Scenario: A China subscription follows the configured provider proxy
    Given a China Kimi provider configured with a local proxy
    When Coddy starts authorization, polls the device and requests refresh, discovery, usage and inference
    Then each China request reaches the configured proxy

  Scenario: Platform requests follow the configured provider proxy
    Given a Kimi Platform provider configured with a local proxy
    When Coddy requests Platform discovery and inference in both regions
    Then each Platform request reaches the configured proxy
