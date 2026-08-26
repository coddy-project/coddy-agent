Feature: Interactive console TUI
  Running coddy with no arguments on a terminal starts an interactive console
  session that renders the conversation with the same machinery every other
  surface uses: the session manager, the agent runner, and ACP updates.

  Background:
    Given a coddy console app over a stub agent runner

  Scenario: Bare start renders the console chrome
    When the console app starts
    Then the screen shows the coddy version header
    And the screen shows the editor between horizontal borders
    And the footer names the configured default model

  Scenario: A prompt streams into the transcript
    When the console app starts
    And the operator submits the prompt "hello there"
    And the stub turn streams the text "Hi! How can I help?"
    Then the transcript shows a user message block containing "hello there"
    And the transcript shows the assistant text "Hi! How can I help?"
    And the footer shows accumulated token usage

  Scenario: A tool call renders as a pending box that completes
    When the console app starts
    And the operator submits the prompt "read the readme"
    And the stub turn starts a tool call named "read" with argument path "README.md"
    Then the transcript shows a pending tool box titled "read"
    When the stub tool call completes with a preview of 14 lines
    Then the tool box shows the preview "preview line 1"
    And the tool box shows the expand hint

  Scenario: The status line names what the turn is doing right now
    When the console app starts
    And the operator submits the prompt "read the readme"
    Then the status line shows "Waiting for the model"
    When the stub turn starts a tool call named "read" with argument path "README.md"
    Then the status line shows "Reading README.md"
    When the stub tool call completes without ending the turn
    Then the status line shows "Waiting for the model"

  Scenario: Ask permission mode renders a modal and allow continues the turn
    Given the session permission mode is "ask"
    When the console app starts
    And the operator submits the prompt "run a command"
    And the stub turn requests permission for the tool "run_command"
    Then the screen shows a permission modal with an allow option
    When the operator confirms the highlighted permission option
    Then the stub turn observes the permission outcome "selected" with option "allow"

  Scenario: Escape cancels the running turn
    When the console app starts
    And the operator submits the prompt "long task"
    And the stub turn blocks until cancelled
    And the operator presses escape
    Then the stub turn observes cancellation
    And the transcript shows an interrupt notice

  Scenario: Model selection through the selector persists on the session
    When the console app starts
    And the operator switches the model to the second configured model
    Then the footer names the second configured model
    And the session state records the second configured model

  Scenario: Reopening a session replays the transcript
    Given a previous console session with the prompt "remember me" and the reply "I remember"
    When the console app starts pinned to that session
    Then the transcript shows a user message block containing "remember me"
    And the transcript shows the assistant text "I remember"

  Scenario: Replayed user rows render as user blocks
    Given a previous console session with the prompt "first question" and the reply "first answer"
    When the console app starts pinned to that session
    Then the replayed prompt "first question" renders as a user message block and not as assistant text

  Scenario: A question with a custom answer accepts typed text
    When the console app starts
    And the operator submits the prompt "ask me something"
    And the stub turn asks a question titled "Pick or type" that allows a custom answer
    And the operator chooses the custom answer and types "my own words"
    Then the stub turn observes the question answer "my own words"

  Scenario: A failed prompt reports the error and the editor recovers
    When the console app starts
    And the operator submits the prompt "boom"
    And the stub turn fails with the error "provider unavailable"
    Then the transcript shows an error notice containing "provider unavailable"
    And the editor accepts new input

  Scenario: Starting a new session drops updates from the old one
    When the console app starts
    And the operator submits the prompt "old session work"
    And the stub turn blocks until cancelled
    And the operator starts a new session
    And the cancelled turn emits a late text chunk "stale text"
    Then the transcript does not show "stale text"
    When the operator submits the prompt "fresh session work"
    And the stub turn streams the text "fresh reply"
    Then the transcript shows the assistant text "fresh reply"

  Scenario: Double ctrl+c exits the console immediately and reports the session
    When the console app starts
    And the operator presses ctrl+c twice
    Then the console app stops within two seconds
    And the exit hint names the session and the continue command

  Scenario: Continuing reopens the most recent session in this folder
    Given a previous console session with the prompt "continue me" and the reply "continued"
    When the console app starts continuing the latest session
    Then the transcript shows a user message block containing "continue me"
    And the transcript shows the assistant text "continued"

  Scenario: A !! command runs locally and stays out of the agent context
    When the console app starts
    And the operator runs the local command "echo hidden-from-the-model"
    Then the transcript shows a local shell block for "echo hidden-from-the-model"
    And the local shell block shows the output "hidden-from-the-model"
    When the operator submits the prompt "what did I just run?"
    And the stub turn streams the text "I have no idea"
    Then no agent turn ever received "hidden-from-the-model"
    And the persisted session carries no trace of "hidden-from-the-model"

  Scenario: The editor marks the local shell prefix while it is typed
    When the console app starts
    And the operator types "!!git status" without sending it
    Then the editor borders render in the local shell color

  Scenario: A one-shot prompt prints the answer and exits
    When the operator runs a one-shot prompt "automation ping"
    And the stub turn streams the text "automation pong"
    Then the one-shot output contains "automation pong"
    And the one-shot run ends cleanly
