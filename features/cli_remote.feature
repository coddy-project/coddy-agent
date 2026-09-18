Feature: Console connected to a remote coddy server
  Bare coddy with --remote points the interactive console (and -p print runs)
  at a remote coddy serve server: turns execute remotely, the transcript
  streams back, and the remote model catalog drives the model selector.

  Scenario: A remote turn streams into the interactive transcript
    Given a fake remote coddy server that answers "remote hello from nas"
    And a console app connected to that remote server
    When the console app starts
    Then the screen shows the remote server banner
    And the footer names the remote default model
    When the operator submits "hi remote"
    Then the transcript shows the assistant text "remote hello from nas"
    And the fake server received a turn for the console session

  Scenario: A background subagent on the server asks in the console attached to it
    Given a fake remote coddy server that answers "the writer keeps working in the background"
    And a console app connected to that remote server
    When the console app starts
    And the operator submits "audit the layout in the background"
    Then the transcript shows the assistant text "the writer keeps working in the background"
    When the server announces that the background subagent "writer" of the console session asks to run "echo checked"
    Then the screen shows a permission modal naming the subagent "writer"
    When the operator confirms the highlighted option
    Then the server receives the answer "allow" for that subagent's child session

  Scenario: Reconnecting closes a permission answered elsewhere while offline
    Given a fake remote coddy server that answers "the writer keeps working in the background"
    And a console app connected to that remote server
    When the console app starts
    And the operator submits "audit the layout in the background"
    Then the transcript shows the assistant text "the writer keeps working in the background"
    When the server announces that the background subagent "writer" of the console session asks to run "echo checked"
    Then the screen shows a permission modal naming the subagent "writer"
    When the connection drops and the permission is settled elsewhere before reconnect
    Then the obsolete permission modal closes without posting an answer

  Scenario: The console lists and stops the background tasks of the remote session
    Given a fake remote coddy server that answers "the build runs in the background"
    And the remote session has a running background task "make test" that printed "ok  pkg/a"
    And a console app connected to that remote server
    When the console app starts
    And the operator submits "/tasks"
    Then the tasks overlay lists the remote task "make test" as running
    When the operator opens the selected task
    Then the tasks overlay shows the remote output "ok  pkg/a"
    When the operator stops the task from the overlay
    Then the server is asked to stop that task
    And the tasks overlay lists the remote task as stopped

  Scenario: The progress of a remote turn leads the console status line
    Given a fake remote coddy server that holds its turn after reporting 1200 generated tokens
    And a console app connected to that remote server
    When the console app starts
    And the operator submits "run the long build"
    Then the console status line shows "1.2k tokens"
    When the fake server lets the turn end
    Then the transcript shows the assistant text "done remotely"

  Scenario: A turn the server woke in the console's session renders in the console
    Given a fake remote coddy server that answers "the build runs in the background"
    And a console app connected to that remote server
    When the console app starts
    And the operator submits "run the build in the background"
    Then the transcript shows the assistant text "the build runs in the background"
    When the server wakes the console session because "make test" failed with exit 2
    Then the transcript shows the assistant text "The build failed with exit 2."
    And the woken turn shows nothing before the agent's answer

  Scenario: A one-shot print run works against the remote server
    Given a fake remote coddy server that answers "printed remotely"
    When the operator runs a remote one-shot prompt "sum it up"
    Then the one-shot output contains "printed remotely"
    And the one-shot run ends cleanly
