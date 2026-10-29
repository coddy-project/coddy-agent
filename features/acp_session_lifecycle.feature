Feature: ACP session lifecycle
  Editors that speak ACP see what a session is called change under their feet,
  can delete a session and resume one without a full load, are told which
  workspace roots a session carries, and answer the agent's questions in a form
  they render themselves.

  Scenario: A session tells the client the title its first message gave it
    Given an ACP client has an active session
    When the client sends a prompt reading "Refactor the auth module"
    Then the client is told the session title is "Refactor the auth module"

  Scenario: A title the agent pins reaches the client as a rename
    Given an ACP client has an active session
    When the session is renamed to "Ship the release"
    Then the client is told the session title is "Ship the release"

  Scenario: A deleted session is gone from the listing
    Given a session with a first prompt was created for the workspace
    When the client deletes the created session
    Then the session list no longer includes the created session

  Scenario: The extra workspace roots of a session are reported back
    Given a session with the workspace as its additional directory was created
    When an ACP client lists the sessions of that workspace
    Then the session list reports the created session's additional directory

  Scenario: A resumed session comes back without its transcript
    Given a session with a first prompt was created for the workspace
    When the client resumes the created session
    Then the resume response is not preceded by a replayed transcript
    And the resume response names a mode

  Scenario: A question reaches a client that renders forms as an elicitation
    Given an ACP client that renders form elicitation has an active session
    When the agent asks "Pick a stack" offering "Go" and "Rust" and the client picks "Rust"
    Then the client was asked "Pick a stack" through elicitation/create with a picker of "Go" and "Rust"
    And the turn hears the answer "Rust"
