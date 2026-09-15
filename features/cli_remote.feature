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

  Scenario: A one-shot print run works against the remote server
    Given a fake remote coddy server that answers "printed remotely"
    When the operator runs a remote one-shot prompt "sum it up"
    Then the one-shot output contains "printed remotely"
    And the one-shot run ends cleanly
