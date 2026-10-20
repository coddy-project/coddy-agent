Feature: An ACP agent as a model, end to end through coddy acp
  coddy acp is the surface an editor drives, and it is also an agent an acp row can run.
  These scenarios run the real coddy acp over stdio, an editor on one side and scripted ACP
  agents on the other, so a turn crosses the session manager, the agent loop, the provider
  and its process pool the way it does for a person: no model and no network.

  Scenario: An editor drives coddy acp on a model an ACP agent serves
    Given coddy acp whose acp rows "alpha" and "beta" run scripted agents
    When an editor opens a session on "alpha/default" and asks "hello"
    Then the editor sees the answer "echo: hello"
    And the editor sees the agent's tool "Read README.md" in the reasoning
    And the session offers the models "alpha/default, alpha/large, beta/default"

  Scenario: The agent's permission request reaches the editor
    Given coddy acp whose acp row "beta" runs an agent that asks to "Write a.txt"
    When an editor opens a session on "beta/default", asks "write it" and allows what it is asked
    Then the editor was asked about "Agent: Write a.txt"
    And the agent heard its allow option

  Scenario: Sessions on two ACP agents answer at once
    Given coddy acp whose acp rows "alpha" and "beta" run agents that answer only 3 prompts at once
    When an editor asks 3 sessions on "alpha/default" and 3 on "beta/default" at once
    Then every session gets the answer to its own question
    And each row ran one agent process for its 3 sessions

  Scenario: A conversation outlives a restart of coddy acp
    Given coddy acp whose acp rows "alpha" and "beta" run scripted agents
    When an editor opens a session on "alpha/default" and asks "one"
    And coddy acp starts again and the editor loads the session and asks "two"
    Then the agent's new session got the conversation with "one" before "two"

  Scenario: A stopped answer stays the agent's own
    Given coddy acp whose acp row "slow" runs an agent that writes "partial" and waits to be stopped
    When an editor opens a session on "slow/default", asks "long job" and stops it once "partial" arrives
    And the editor asks "next"
    Then the agent's next prompt carried only "next"

  Scenario: ACP through ACP
    Given coddy acp whose acp row "coddy" runs another coddy acp serving the acp row "inner"
    When an editor opens a session on "coddy/default" and asks "hi"
    Then the editor sees the answer "echo: hi"
    And the editor sees the agent's tool "Grep TODO" in the reasoning
    And the inner agent ran two levels down
    And the row "coddy" lists the models "default, inner/default, inner/large"

  Scenario: A permission request travels two agents up
    Given coddy acp whose acp row "coddy" runs another coddy acp serving an agent that asks to "Edit main.go"
    When an editor opens a session on "coddy/default", asks "edit it" and allows what it is asked
    Then the editor was asked about "Agent: Edit main.go"
    And the agent heard its allow option
