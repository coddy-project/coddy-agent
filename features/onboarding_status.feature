Feature: Onboarding status follows the agent's provider
  The embedded UI opens the provider picker when the configured agent cannot
  run a turn. A ready local endpoint needs no API key, and an unused provider
  without credentials must not reopen the picker after it was dismissed.

  @http
  Scenario: A ready local provider needs no key
    Given a Coddy config with a ready local agent provider and a keyless spare provider
    When the onboarding status is requested
    Then the onboarding status keeps the provider picker closed
    And the onboarding status lists "spare" among the providers without credentials

  @http
  Scenario: The agent's own provider has no credentials
    Given a Coddy config whose OpenAI agent provider has no credentials
    When the onboarding status is requested
    Then the onboarding status asks for the provider picker
