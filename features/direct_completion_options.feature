Feature: A direct model applies the generation options of a chat completion request
  A models[].model id on POST /v1/chat/completions is coddy standing in for
  the provider, so the max_tokens and temperature a client sends are what the
  provider receives for that request, and without them the model's configured
  values apply. The options belong to the one request: the configuration is
  left as it was, and the next request starts from it again.

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
