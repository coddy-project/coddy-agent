Feature: The settings form fetches the model list a provider advertises
  The logical-models editor offers the models a provider serves instead of
  making the operator type the ids by hand. The provider row being edited has
  not necessarily been saved yet - the sign-in fields accept such rows, and the
  models fetch does too: the row travels in the request body of
  POST /coddy/providers/models (issue #335), so a provider that exists only in
  the form is fetched the same way as a stored one. Fields the body leaves
  empty are inherited from a saved provider of the same name, so a sparse
  {"name": "..."} post resolves the stored credentials without secrets
  travelling over the wire.

  Scenario: A provider row that is not saved yet lists its models
    Given an upstream model endpoint serving "m1,m2"
    And a coddy server holding only a provider named "other"
    When the settings form posts the provider row "fresh" of type "openai" at that upstream with key "sk-fresh"
    Then the gateway answers with the models "m1,m2"
    And the upstream saw the key "sk-fresh"

  Scenario: The listing carries the context window the provider reports
    Given an upstream model endpoint serving "m1:131072,m2:8192"
    And a coddy server holding only a provider named "other"
    When the settings form posts the provider row "fresh" of type "openai" at that upstream with key "sk-fresh"
    Then the gateway answers with the models "m1,m2"
    And the gateway answers with context window 131072 for "m1"
    And the gateway answers with context window 8192 for "m2"

  Scenario: A provider pointed at another Coddy reads the windows that Coddy serves
    GET /v1/models of a coddy serve reports each model's window as
    max_context_tokens, and as context_length, the spelling OpenAI-compatible
    clients read. A Coddy whose provider is that API measures its models
    against those windows, not against the 128000-token fallback (issue #575).
    Given a source coddy server serving "big:32768,small:8192"
    And a coddy server whose provider "src" at that source lists the models "local/big,local/small"
    Then the model list reports the context window 32768 for "src/local/big"
    And the model list reports the context window 8192 for "src/local/small"
    When the settings form posts only the provider name "src"
    Then the gateway answers with context window 32768 for "local/big"
    And the gateway answers with context window 8192 for "local/small"

  Scenario: The models catalog fills in a model the provider reports no window for
    Coddy carries a table of model ids with a context window, a max_tokens
    to start from and whether the model reads images, built from models.dev
    and embedded into the binary (issue #554). The settings form fills a model it adds from it when
    the provider reports no window for the id. A window the provider reports
    stands alone: the catalog's max_tokens was worked out for the catalog's
    window and fits no other.
    Given an upstream model endpoint serving "gpt-4o,claude-sonnet-4-5:1000000,m2"
    And a coddy server holding only a provider named "other"
    When the settings form posts the provider row "fresh" of type "openai" at that upstream with key "sk-fresh"
    Then the gateway answers with the window, max_tokens and image support the models catalog has for "gpt-4o"
    And the gateway answers with context window 0 for "gpt-4o"
    And the gateway answers with context window 1000000 for "claude-sonnet-4-5"
    And the gateway answers with nothing from the models catalog for "claude-sonnet-4-5"
    And the gateway answers with nothing from the models catalog for "m2"

  Scenario: A codex provider lists the models only recent Codex releases are offered
    The Codex backend leaves a model out of its catalog for every Codex
    release older than the model's minimal_client_version, and it answers a
    Codex source build (client_version 0.0.0) as an older release. Coddy runs
    every model through its own loop and tools, so the settings form lists
    the models the newest Codex releases are offered too (issue #394).
    Given a stand-in Codex backend whose catalog offers "gpt-6-astra" from Codex 0.153.0
    And the Codex catalog offers "gpt-6-sol,gpt-6-luna" from Codex 0.155.0
    And a coddy server holding a codex provider signed in to that backend
    When the settings form posts only the provider name "codex"
    Then the gateway answers with the models "gpt-6-astra,gpt-6-luna,gpt-6-sol"
    When the settings form reads the models of the saved provider "codex"
    Then the gateway answers with the models "gpt-6-astra,gpt-6-luna,gpt-6-sol"

  Scenario: A codex model measures its context against the window of the Codex catalog
    The Codex catalog reports each model's context window, context_window,
    the window Codex itself works with (272000 for every model it served on
    2026-09-27). A codex model without max_context_tokens measures its context
    ring and its automatic compaction against that window, not against the
    128000-token fallback.
    Given a stand-in Codex backend whose catalog offers "gpt-6-astra" from Codex 0.153.0
    And a coddy server holding a codex provider signed in to that backend
    When the settings form posts only the provider name "codex"
    Then the gateway answers with context window 272000 for "gpt-6-astra"
    And the model list reports the context window 272000 for "codex/gpt-6-astra"

  Scenario: A Claude model measures its context against the window Anthropic's listing reports
    Anthropic's GET /v1/models reports each model's input window as
    max_input_tokens. A Claude model without max_context_tokens measures its
    context ring and its automatic compaction against that window, not
    against the 128000-token fallback. The settings form still fills a model
    it adds with the catalog's max_tokens and image support when Anthropic's
    window is the catalog's.
    Given an Anthropic model endpoint serving "claude-sonnet-4-5:catalog,seed-model:150000"
    And a coddy server holding a provider "claude" of type "anthropic" at that upstream with key "sk-ant"
    When the settings form posts only the provider name "claude"
    Then the gateway answers with context window 150000 for "seed-model"
    And the gateway answers with the window, max_tokens and image support the models catalog has for "claude-sonnet-4-5"
    And the model list reports the context window 150000 for "claude/seed-model"
