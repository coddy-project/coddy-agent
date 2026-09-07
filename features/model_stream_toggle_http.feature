Feature: The HTTP gateway serves a non-streaming model end to end
  A models[] entry with "stream: false" changes only how coddy talks to the LLM
  backend. The gateway keeps its own contract with clients: a request that asked
  for an SSE stream still receives session updates and a completed response, and
  the session transcript is written exactly as it is for a streamed model.

  Scenario: A ReAct turn on a non-streaming model streams to the client and runs its tools
    Given a coddy gateway whose agent model is backed by a stub server that refuses streaming requests
    When a client sends a streaming prompt over POST /v1/responses
    Then the stub server received only non-streaming chat completion requests
    And the client received the answer over SSE
    And the workspace tool call ran and its result reached the final assistant message
    And the session transcript ends with that assistant message
