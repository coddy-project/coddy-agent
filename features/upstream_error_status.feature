Feature: A provider's failure keeps its HTTP status on the way to the client
  When the model's provider rejects a request, the status it answered with
  tells an OpenAI-compatible client whether to retry, back off or give up.
  Coddy passes that status on instead of answering 500 for every failure: a
  4xx of the provider reaches the client as the same 4xx, and the error body
  names it as upstream_status next to type upstream_error, so no client has
  to read the message to learn it.

  Scenario: A provider's 400 reaches a blocking client as 400
    Given a coddy server whose direct model's provider answers 400 "Request too long: 121692 input tokens over the limit of 46112"
    When an OpenAI client posts "local/llama-3.1-8b" without streaming
    Then the answer is HTTP 400
    And the error is an upstream_error with upstream_status 400 and the provider's message

  Scenario: A streamed completion names the provider's status in its error frame
    Given a coddy server whose direct model's provider answers 400 "Request too long: 121692 input tokens over the limit of 46112"
    When an OpenAI client streams "local/llama-3.1-8b"
    Then the stream's error frame is an upstream_error with upstream_status 400 and the provider's message
