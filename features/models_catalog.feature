Feature: Settings suggests model metadata from the models catalog
  api.openai.com and Anthropic report no context window in their model
  listings, so a model added from them used to fall back to 128000 unless the
  operator typed the number. The settings screen now asks the models catalog
  (models.dev's api.json, or the mirror models_catalog.url names) what it
  knows of the models a provider row lists: the context window, a max_tokens
  that still lets every request fit, and whether the model reads images. The
  values are suggestions: they reach config.yaml only when the operator adds
  the model or applies one.

  Scenario: A Claude model is suggested its window, max_tokens and image support
    Given a models catalog that lists "claude-sonnet-4-5" under "anthropic" with a 200000 window, a 64000 output limit and image input
    And the settings screen edits a provider row of type "anthropic"
    When the settings screen asks the catalog about "claude-sonnet-4-5"
    Then the catalog matches the row to "anthropic"
    And it suggests a context window of 200000, max_tokens of 40000 and image support

  Scenario: The window the provider reports is what max_tokens is measured against
    Given a models catalog that lists "claude-sonnet-4-5" under "anthropic" with a 200000 window, a 64000 output limit and image input
    And the settings screen edits a provider row of type "anthropic"
    When the settings screen asks the catalog about "claude-sonnet-4-5" whose provider reports a 100000 window
    Then it suggests a context window of 200000, max_tokens of 20000 and image support
