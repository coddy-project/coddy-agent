Feature: A local ACP agent served as a model
  A provider of type acp runs an Agent Client Protocol agent on stdio (claude-agent-acp,
  codex-acp, opencode acp, coddy acp) and serves it as a model. The agent brings its own
  instructions and tools; a session's turns go to one ACP session of the agent, and what
  the agent asks permission for reaches the person who holds the session.

  Scenario: The agent's answer streams back
    Given an acp provider whose agent reads a file and answers "Hello"
    When a session asks "hi"
    Then the answer streams as "Hello"
    And the reasoning names the agent's tool "Read README.md"
    And the agent did not receive Coddy's system prompt

  Scenario: A conversation continues on one agent session
    Given an acp provider whose agent echoes what it is asked
    When a session asks "one"
    And the same session asks "two"
    Then the agent opened one session
    And the second prompt carried only "two"

  Scenario: The model is chosen among the agent's own
    Given an acp provider whose agent offers the models "small, large"
    And the provider's model is "large"
    When a session asks "hi"
    Then the agent's model option was set to "large"

  Scenario: The agent asks for permission and the person answers
    Given an acp provider whose agent asks permission to "Write a.txt"
    When a session asks "write it" and the person allows it
    Then the person was asked about "Write a.txt"
    And the agent heard its own allow option

  Scenario: The agent's tool is named by what it works on
    Given an acp provider whose agent calls its tool "read" and names the file "docs/notes.txt" only once it runs
    When a session asks "read the notes"
    Then the reasoning names the agent's tool "read docs/notes.txt"
