Feature: A model behind an OpenRouter-style aggregator shows its reasoning and what it cost
  OpenRouter and the services built like it (RouterAI) put a model's reasoning
  in the reasoning field of a message, where vLLM and llama.cpp write
  reasoning_content, and report what the reasoning cost in
  usage.completion_tokens_details.reasoning_tokens. Their model listing names
  the request parameters each model takes, and many of their models take the
  unified reasoning object rather than OpenAI's reasoning_effort. A model id
  there carries its vendor in front (qwen/qwen3.8-flash), and the reasoning
  levels follow the model behind it. Before this, such a model offered no
  levels, was never asked to reason, and whatever reasoning it sent was dropped
  on the way to the chat (issue #258).

  Background:
    Given an aggregator serving "qwen/qwen3.8-flash" that takes the reasoning object and not reasoning_effort
    And a coddy server whose model "agg/qwen/qwen3.8-flash" runs on that aggregator

  Scenario: The model offers reasoning levels under its vendor-prefixed id
    When a client lists the models
    Then "agg/qwen/qwen3.8-flash" offers the reasoning levels "low,medium,high"

  Scenario: A turn asks for reasoning the way the listing says and shows it with its cost
    When a coddy client streams a turn at reasoning level "high"
    Then the aggregator was asked for reasoning with effort "high" and without reasoning_effort
    And the stream carries the reasoning "Weighing the request." as reasoning_content
    And the stream's token_usage reports 42 reasoning tokens
    And the session history keeps the reasoning "Weighing the request." with 42 reasoning tokens
