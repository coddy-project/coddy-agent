Feature: Manual context compaction command
  The user compacts a long session on demand. The built-in /compact command
  works over every prompt surface (ACP session/prompt, HTTP /v1/responses),
  and a REST endpoint compacts a session directly. The transcript keeps all
  original messages and gains a summary row; the command itself never becomes
  part of the conversation history.

  Background:
    Given a running coddy HTTP server with a summarizing agent
    And an HTTP session with 4 completed exchanges

  Scenario: The /compact command compacts the session over the prompt surface
    When the user sends "/compact" as a prompt
    Then the prompt response confirms the compaction
    And the session transcript contains a compaction summary row
    And the session transcript still contains all 4 original exchanges
    And the "/compact" command is not part of the transcript

  Scenario: The REST endpoint compacts the session directly
    When the client posts to the session compact endpoint
    Then the compact request succeeds
    And the compact response reports the summary and message counts
    And the session transcript contains a compaction summary row
