Feature: Compatible provider client identity
  Scenario Outline: Select the identity sent to the provider
    Given an "<protocol>" provider with user agent "<identity>"
    When completions and model listing are requested
    Then all requests identify the client as "<prefix>"

    Examples:
      | protocol  | identity  | prefix       |
      | openai    |           | OpenAI/Go    |
      | openai    | openai    | OpenAI/Go    |
      | openai    | coddy     | Coddy/       |
      | anthropic |           | Anthropic/Go |
      | anthropic | anthropic | Anthropic/Go |
      | anthropic | coddy     | Coddy/       |
      | openai    | anthropic | OpenAI/Go    |
      | anthropic | openai    | Anthropic/Go |
