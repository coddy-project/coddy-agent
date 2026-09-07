Feature: The HTTP gateway serves the read-only ask mode end to end
  The ask profile is a session mode like agent and plan: POST /v1/responses with
  model "ask" runs the ReAct loop over the real OpenAI-compatible transport with
  the read-only tool set, and a mutating call the model emits anyway is refused
  on its way to the tool. Agent and plan keep the tool sets they had, so the
  third mode changes nothing for the two that already exist.

  Scenario: An ask turn reads the workspace and answers over SSE
    Given a coddy gateway backed by a streaming stub model that reads a file, then answers
    When a client sends a streaming "ask" prompt over POST /v1/responses
    Then the model was offered only read-only tools
    And the client received the answer over SSE with the file contents
    And the transcript reports the session in "ask" mode

  Scenario: A write the model emits in ask mode is refused before it runs
    Given a coddy gateway backed by a streaming stub model that writes a file, then answers
    When a client sends a streaming "ask" prompt over POST /v1/responses
    Then the workspace file was not written
    And the stream carries a cancelled tool call with the ask-mode refusal
    And the model was re-prompted with that refusal as the tool result

  Scenario: The same model in agent mode still writes the file
    Given a coddy gateway backed by a streaming stub model that writes a file, then answers
    When a client sends a streaming "agent" prompt over POST /v1/responses
    Then the workspace file was written
    And the model was offered the full agent tool set
    And the transcript reports the session in "agent" mode

  Scenario: Plan mode keeps its own tool set
    Given a coddy gateway backed by a streaming stub model that reads a file, then answers
    When a client sends a streaming "plan" prompt over POST /v1/responses
    Then the model was offered the plan tool set
    And the client received the answer over SSE with the file contents
    And the transcript reports the session in "plan" mode
