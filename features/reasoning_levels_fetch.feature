Feature: Settings fetches the reasoning levels a model id offers
  Reasoning levels are auto-detected from the API model id, so an operator adding a
  logical model would otherwise have to know each family's tiers by heart. The
  settings form asks the gateway instead: GET /coddy/config/reasoning-levels reports
  the levels that model id offers when no reasoning_levels override is configured,
  under the same provider-aware remap the composer and GET /v1/models apply. The
  model id travels as a query parameter because the entry being edited has not been
  saved yet.

  Scenario: A Qwen3 model id offers the standard reasoning tiers
    Given a coddy gateway with an "openai" provider named "valera"
    When the settings form fetches the reasoning levels for "valera/qwen3.8-27b"
    Then the gateway answers with the levels "low,medium,high"
    And the answer reports the levels as detected

  Scenario: A codex-backed gpt-5 id offers none in place of minimal
    Given a coddy gateway with a "codex" provider named "codex"
    When the settings form fetches the reasoning levels for "codex/gpt-5.5"
    Then the gateway answers with the levels "none,low,medium,high"
    And the answer reports the levels as detected

  Scenario: A model without reasoning support answers with an empty list
    Given a coddy gateway with an "openai" provider named "valera"
    When the settings form fetches the reasoning levels for "valera/gpt-4o"
    Then the gateway answers with no levels
    And the answer reports the levels as not detected
