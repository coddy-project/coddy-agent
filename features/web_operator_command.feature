Feature: The web UI shows an operator command and runs it on Run
  A turn the web UI streams declares that it answers operator commands. When
  the agent asks the operator to run a command, the stream carries the
  request as an operator_command event, the operator's Run goes back through
  POST /coddy/sessions/{id}/operator-command, the command's output streams on
  the tool call's frames, and the model reads it in its next request. A
  streaming client that declared nothing is never offered the tool.

  Scenario: The operator presses Run in the web UI and the model reads the output
    Given a web server whose model asks the operator to run "echo web-operator-ok"
    When the web UI streams a turn that declares operator commands
    And the operator answers the operator_command event with run
    Then the stream carried the command "echo web-operator-ok" with its shell, host and folder
    And the stream carried the run of the command until it ended
    And the model's next request carried the output "web-operator-ok"

  Scenario: A streaming client that does not declare operator commands is not offered the tool
    Given a web server whose model asks the operator to run "echo never"
    When a client streams a turn without declaring operator commands
    Then the model was not offered operator_command
