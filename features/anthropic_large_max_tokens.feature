Feature: Anthropic models answer one-shot requests whatever their max_tokens
  A one-shot request - the compaction summary, the turns of a model with
  "stream: false" - waits for the whole answer before it returns. The Anthropic
  client refuses such a request on its own, before anything is sent, once
  max_tokens promises an answer longer than ten minutes of generation
  (above 21333 tokens). Coddy keeps the model's max_tokens and still gets the
  answer: a one-shot request of a streaming model is read from a stream, and a
  model configured not to stream sends one blocking request with a deadline
  sized to its max_tokens.

  Scenario: A one-shot request of a streaming model with a large max_tokens
    Given an Anthropic model with max_tokens 40000 pointed at a stub server
    When Coddy asks the model for one complete answer
    Then the answer is "Summary of the session."
    And the stub server received 1 request with "stream" set to true and max_tokens 40000

  Scenario: A model configured not to stream with a large max_tokens
    Given an Anthropic model with max_tokens 40000 and streaming disabled pointed at a stub server
    When Coddy streams the model's answer
    Then the answer is "Summary of the session."
    And the stub server received 1 request with "stream" unset and max_tokens 40000
