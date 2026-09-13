Feature: A chat conversation is an ordinary session
  Where a person talks to the agent decides nothing about the session behind the
  conversation. A chat opened from Telegram gets the same kind of id a console or
  a browser session gets, and nothing about the messenger is written into the
  transcript.

  What the messenger needs is told to the model for the turn instead, as a block
  of the system prompt, and applied to the answer on its way out of the gateway.
  Both belong to the surface: the next integration describes its own quirks and
  renders its own syntax, and the conversation it leaves behind reads like any
  other.

  Background:
    Given a telegram gateway over a scripted agent

  Scenario: The session behind a chat carries an ordinary session id
    When the user sends "hello"
    Then the session behind the chat has an ordinary session id

  Scenario: The transcript keeps what the user wrote and nothing else
    When the user sends "hello"
    Then the agent was prompted with exactly "hello"
    When the user sends "and again"
    Then the agent was prompted with exactly "and again"

  Scenario: The model is told how to answer through this messenger
    When the user sends "hello"
    Then the turn carried a system prompt block naming Telegram
    And that block describes the answer format
    And nothing of it reached the message the agent was prompted with

  Scenario: The answer is rendered for the messenger on its way out
    Given the agent answers with "## Findings\n\n**two** of them"
    When the user sends "hello"
    Then the chat received "*Findings*"
    And the chat received "*two* of them"
    And the chat received no text containing "##"

  Scenario: A fenced code block reaches the chat as the agent wrote it
    Given the agent answers with a fenced code block holding "**stars** and # hash"
    When the user sends "hello"
    Then the chat received "**stars** and # hash"
