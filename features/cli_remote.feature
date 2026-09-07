Feature: Console connected to a remote coddy server
  Bare coddy with --remote points the interactive console (and -p print runs)
  at a remote coddy http server: turns execute remotely, the transcript
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

  Scenario: A one-shot print run works against the remote server
    Given a fake remote coddy server that answers "printed remotely"
    When the operator runs a remote one-shot prompt "sum it up"
    Then the one-shot output contains "printed remotely"
    And the one-shot run ends cleanly
