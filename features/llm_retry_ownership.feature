Feature: Coddy owns LLM retries
  Coddy's configured retry limit is the single source of truth for upstream
  request attempts. Provider SDKs must not add hidden HTTP retries.

  Scenario Outline: One configured retry sends exactly two upstream requests
    Given a "<provider>" provider using "<mode>" requests with one configured retry
    When the upstream responds with a retryable server error
    Then the provider call fails
    And exactly 2 upstream requests are sent

    Examples:
      | provider  | mode          |
      | openai    | non-streaming |
      | openai    | streaming     |
      | anthropic | non-streaming |
      | anthropic | streaming     |
      | codex     | non-streaming |
      | codex     | streaming     |
