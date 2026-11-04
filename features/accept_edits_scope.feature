Feature: accept_edits approves the edits of the project
  The accept_edits permission mode lets the agent edit the project without a
  prompt for every file. The agent's home is not the project: hooks.json,
  mcp.json, config.yaml and the trust receipts decide what runs and what is
  allowed, which is why config_commit asks even under accept_edits. A write
  there, or anywhere else outside the workspace, asks the same way.

  Scenario: An edit inside the workspace runs without a prompt
    Given a session in permission mode "accept_edits"
    When the model writes the file "notes.txt" in the workspace
    Then the client received 0 permission requests

  Scenario: A write into the agent's home asks
    Given a session in permission mode "accept_edits"
    When the model writes the file "hooks.json" in the agent's home
    Then the client received 1 permission request

  Scenario: A write outside the workspace asks
    Given a session in permission mode "accept_edits"
    When the model writes the file "notes.txt" outside the workspace
    Then the client received 1 permission request

  Scenario: bypass still writes anywhere without a prompt
    Given a session in permission mode "bypass"
    When the model writes the file "hooks.json" in the agent's home
    Then the client received 0 permission requests
