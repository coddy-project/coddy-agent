Feature: A user's model choice lasts for the session
  The agent changes its model only when the user asks and keeps that choice
  for later turns unless the user limits it.

  Scenario: A requested model change applies to the next request and later turns
    Given a session with models "fake/a" and "fake/b"
    When the user asks the agent to switch to "fake/b"
    And the user sends another message
    Then the first model served one request and the chosen model served two
    And the session remembers "fake/b"
