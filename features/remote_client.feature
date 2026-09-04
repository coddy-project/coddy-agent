Feature: Remote client for the coddy HTTP API
  A coddy process can act as a client of a remote coddy http server: the
  console and ACP surfaces create sessions, stream prompts, replay
  transcripts, and answer permission requests over HTTP instead of running
  the agent locally.

  Background:
    Given a remote coddy server protected by the token "secret-token"
    And a remote client connected with the token "secret-token"

  Scenario: A prompt streams the assistant answer through the client
    Given the remote agent replies with "remote says hi"
    And the client starts a session
    When the client sends the prompt "hello over http"
    Then the client receives the streamed text "remote says hi"
    And the turn ends with stop reason "end_turn"
    And the session is persisted on the remote server

  Scenario: The remote model catalog backs the session options
    When the client starts a session
    Then the session model options come from the remote server catalog

  Scenario: A permission request round-trips through the client
    Given the remote agent asks permission before replying "guarded answer"
    And the client answers permissions with "allow"
    And the client starts a session
    When the client sends the prompt "run the tool"
    Then the client receives the streamed text "guarded answer"

  Scenario: Loading a remote session replays its transcript
    Given the remote agent replies with "first answer" and records a tool call
    And the client starts a session
    And the client sends the prompt "first question"
    When a fresh client loads that session
    Then the replay contains the user text "first question"
    And the replay contains the agent text "first answer"
    And the replay contains a completed tool call

  Scenario: The newest remote session comes first in the list
    Given the remote agent replies with "remote says hi"
    And the client starts a session
    And the client sends the prompt "older"
    And the client starts a session
    And the client sends the prompt "newer"
    When the client lists remote sessions
    Then the first listed session is the newest one

  Scenario: Loading a remote plan session keeps the plan profile
    Given the remote agent replies with "plan sketch"
    And the client starts a session
    And the client sends the prompt "draft a plan"
    And the remote session is switched to plan mode
    When a fresh client loads that session
    Then the loaded session mode is "plan"

  Scenario: The server stop reason reaches the client
    Given the remote agent replies with "capped" and stops at the turn limit
    And the client starts a session
    When the client sends the prompt "long job"
    Then the turn ends with stop reason "max_turns"

  Scenario: A subagent's permission prompt reaches the remote client even when the server bypasses its own
    Given the remote server runs with permission mode "bypass"
    And the remote agent relays a subagent permission prompt before replying "child allowed"
    And the client answers permissions with "allow"
    And the client starts a session
    When the client sends the prompt "delegate the reading"
    Then the client saw a tool call named "spawn_agent"
    And the client received a permission request titled "[subagent explore] Run: run_command"
    And the client receives the streamed text "child allowed"
