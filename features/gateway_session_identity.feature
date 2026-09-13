Feature: A chat conversation is an ordinary session
  Where a person talks to the agent decides nothing about the session behind the
  conversation. A chat opened from Telegram gets the same kind of id a console or
  a browser session gets, and nothing about the messenger is written into the
  transcript: the syntax one messenger understands is applied to the answer on its
  way out of the gateway, where the next integration will apply its own.

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
