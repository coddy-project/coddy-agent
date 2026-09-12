Feature: POST /v1/chat/completions streams the contract an OpenAI client parses
  The OpenAI-compatible endpoint is the one surface whose reader is not ours:
  VS Code Copilot wired in through chatLanguageModels.json, the openai SDKs,
  anything that reads chat.completion.chunk literally. Such a client ends a
  completion on the chunk that carries finish_reason and treats every data:
  line as a chunk, so a stream that never finishes its choice leaves the client
  with nothing to show - "Response contained no choices." in VS Code - and
  coddy's named event: frames are read as chunks that have no choices at all.

  Coddy's own rich channel is POST /v1/responses, which the SPA, the remote
  console and the swarm relay read. It keeps every named event.

  Scenario: A streamed direct completion finishes its choice
    Given a coddy server whose model streams reasoning before its answer
    When an OpenAI client streams "local/qwen3-1.7b" over POST /v1/chat/completions
    Then the client assembles the answer "Hi there."
    And the stream opens with an assistant role chunk
    And every chunk carries a finish_reason field
    And the last chunk before [DONE] finishes with "stop"
    And the stream carries no named SSE events
    And the streamed reasoning "Thinking it over." survives as reasoning_content

  Scenario: A streamed agent turn finishes its choice
    Given a coddy server whose model streams reasoning before its answer
    When an OpenAI client streams "agent" over POST /v1/chat/completions
    Then the client assembles the answer "Hi there."
    And the last chunk before [DONE] finishes with "stop"
    And the stream carries no named SSE events

  Scenario: A client that asks for usage is given a final usage chunk
    Given a coddy server whose model streams reasoning before its answer
    When an OpenAI client streams "local/qwen3-1.7b" over POST /v1/chat/completions asking for usage
    Then the last chunk before [DONE] reports 12 input and 5 output tokens
    And that usage chunk carries an empty choices array

  Scenario: The rich coddy channel is untouched on POST /v1/responses
    Given a coddy server whose model streams reasoning before its answer
    When a coddy client streams "agent" over POST /v1/responses
    Then the stream carries named SSE events
    And the stream ends with coddy_meta before [DONE]
