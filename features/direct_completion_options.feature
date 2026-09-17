Feature: A direct model applies the generation options of a chat completion request
  A models[].model id on POST /v1/chat/completions is coddy standing in for
  the provider, so the max_tokens, temperature and reasoning_effort a client
  sends are what the provider receives for that request, and without them the
  model's configured values apply: its max_tokens and temperature, and the
  reasoning_default GET /v1/models reports for it. The options belong to the
  one request: the configuration is left as it was, and the next request
  starts from it again.

  Scenario: The request's max_tokens and temperature reach the provider
    Given a coddy server whose direct model is configured with max_tokens 8192 and temperature 0.2
    When an OpenAI client streams "local/llama-3.1-8b" with max_tokens 256 and temperature 0.7
    Then the upstream request carried max_tokens 256 and temperature 0.7
    And the model is still configured with max_tokens 8192 and temperature 0.2

  Scenario: The next request without options runs on the configured values
    Given a coddy server whose direct model is configured with max_tokens 8192 and temperature 0.2
    When an OpenAI client streams "local/llama-3.1-8b" with max_tokens 256 and temperature 0.7
    And an OpenAI client streams "local/llama-3.1-8b" without generation options
    Then the upstream request carried max_tokens 8192 and temperature 0.2

  Scenario: The request's reasoning_effort reaches the provider
    Given a coddy server whose direct model "local/o4-mini" reasons at "medium" by default
    When an OpenAI client streams "local/o4-mini" with reasoning_effort "high"
    Then the provider received the reasoning level "high"

  Scenario: Without reasoning_effort the model's reasoning default applies and is reported
    Given a coddy server whose direct model "local/o4-mini" reasons at "medium" by default
    When an OpenAI client posts "local/o4-mini" without reasoning_effort
    Then the provider received the reasoning level "medium"
    And the JSON answer reports reasoning_effort "medium"

  Scenario: A codex model receives the requested level, "none" included
    Given a coddy server whose direct model "codex/gpt-5.5" is a codex model reasoning at "medium" by default
    When an OpenAI client streams "codex/gpt-5.5" with reasoning_effort "none"
    Then the provider received the reasoning level "none"
