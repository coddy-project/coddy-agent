Feature: An ACP agent as a model behind the web UI's API
  The web UI and the remote console reach an acp row through the HTTP API of coddy serve:
  the model listings, a streamed chat, the permission prompt and the subagents a chat
  starts. The rows run scripted ACP agents and the chat's own model is a scripted
  OpenAI-compatible server, so nothing leaves the machine.

  Scenario: The models of acp rows are listed
    Given a server whose acp rows "alpha" and "beta" run scripted agents
    Then the model list offers "alpha/default, alpha/large, beta/default"
    And the models route lists "default, small, large" for the row "alpha"

  Scenario: A chat streams the agent's answer and draws its tools as rows that outlive a reload
    Given a server whose acp rows "alpha" and "beta" run scripted agents
    When a chat on "alpha/default" asks "hello"
    Then the stream carries the answer "echo: hello"
    And the stream draws the agent's tool "Read README.md" as a row with the output "line 1"
    And the reloaded chat draws the agent's tool "Read README.md" with the output "line 1" cut short
    And the whole output of the agent's tool "Read README.md" is served

  Scenario: The person answers the agent's permission request through the permission route
    Given a server whose acp row "beta" runs an agent that asks to "Write a.txt"
    When a chat on "beta/default" asks "write it" and the person allows the request
    Then the stream asked about "Agent: Write a.txt"
    And the chat's answer says the agent heard its allow option

  Scenario: Chats on two ACP agents stream at once
    Given a server whose acp rows "alpha" and "beta" run agents that answer only 3 prompts at once
    When 3 chats on "alpha/default" and 3 on "beta/default" ask at once
    Then every chat streams the answer to its own question

  Scenario: A subagent runs on an ACP model
    Given a server whose acp rows "alpha" and "beta" run scripted agents
    And the chat's own model hands "check the repo" to a subagent on "alpha/default"
    When a chat on "stub/coddy-demo" asks "check the repo"
    Then the subagent's report holds "echo: child task"
