Feature: An ACP agent as the model of a Telegram chat
  A chat of the bot runs on an acp row like on any other model: the bot streams what the
  agent writes into the chat. The agent's own tools are not Coddy's, so the bot does not
  wave its permission requests through the way it does for the chat agent's tools: it asks
  in the chat with buttons, as it does for a subagent, and the tap decides.

  Scenario: The bot answers with what the agent wrote
    Given a telegram chat whose model is an acp row with an agent that echoes
    When the admin writes "ping the agent"
    Then the chat shows "echo: ping the agent"

  Scenario: The admin answers the agent's permission request in the chat
    Given a telegram chat whose model is an acp row with an agent that asks to "Write a.txt"
    When the admin writes "write it"
    Then the chat shows a permission request for "Agent: Write a.txt" with the buttons "Allow" and "Reject"
    When the admin taps "Allow"
    Then the chat shows "permission: ok"
