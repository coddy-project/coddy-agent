Feature: Codex ChatGPT credentials drive Coddy's own agent
  A "codex" provider is an LLM backend, not a different agent: Coddy signs in
  with ChatGPT (or reuses a Codex CLI login), sends the resulting OAuth access
  token to the Codex Responses backend, and keeps its own system prompt, its
  own tools, and its own ReAct loop. Covered end to end on both surfaces so the
  credential source is observable: the HTTP gateway signs in through the device
  flow and uses the Coddy-managed credential, the ACP flow falls back to the
  Codex CLI login in CODEX_HOME.

  @openai
  Scenario: HTTP sign-in stores a credential that authenticates model listing and an agent turn
    Given a coddy HTTP server with a codex provider and a stand-in Codex backend
    When I sign in with ChatGPT through the device flow over REST
    Then the codex provider reports connected with source "coddy"
    And the provider model list is fetched with the signed-in token
    When I send an agent prompt over POST /v1/responses
    Then the Codex backend received the signed-in access token
    And the Codex request carried coddy's own tools and system prompt
    And the final assistant message contains the coddy tool result

  @acp
  Scenario: ACP turn falls back to the Codex CLI login and keeps its chain of thought
    Given an ACP session manager with a codex provider and a Codex CLI login only
    When I run an agent prompt through the ACP session flow
    Then the Codex backend received the Codex CLI access token
    And the Codex request carried coddy's own tools and system prompt
    And the second Codex request replayed the encrypted reasoning of the first
    And the final assistant message contains the coddy tool result
