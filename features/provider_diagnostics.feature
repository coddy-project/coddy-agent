Feature: Telling the user which provider is misconfigured
  A provider is a name in config.yaml, and the address behind that name is not
  always the one the user has in mind: type openai without api_base means the
  official OpenAI endpoint, and with no key there it answers 401 to everything.
  So a failed call opens with the provider name and the address the request
  actually went to, and the provider list says up front when a provider is
  aimed at OpenAI with nothing to authenticate with.

  Scenario: A failed call names the provider and the address it went to
    Given a provider named "rpa" of type openai whose endpoint rejects every request
    When a completion is streamed through that provider
    Then the notice names the provider "rpa"
    And the notice names the address of that endpoint
    And the message from the endpoint survives in the notice

  Scenario: The provider list warns about an openai provider with no address and no key
    Given a config whose only provider is "openai" of type openai with no api_base and no key
    When I run the provider list
    Then the list warns that the provider talks to the official OpenAI endpoint
    And the warning says no credential is configured
    And the warning names the provider "openai"
