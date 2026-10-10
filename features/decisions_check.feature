Feature: Decisions command safety check
  A shell command that would run without a permission prompt - bypass mode,
  the command allowlist, a session grant or a hook's allow - is asked about
  on the NeuralDeep decisions endpoint before it runs. A remote command of
  ssh_run_command is asked about the same way in bypass mode, where every
  surface answers its prompt by itself. A command the endpoint
  classifies as unsafe is rejected: the refusal is the call's tool result, so
  the session records why the command never ran and the model can choose
  another path. A command the operator approved in a prompt is not checked
  again: the human in the loop is the stronger verdict.

  Scenario: An unsafe command is rejected in bypass mode
    Given a coddy session in agent mode under bypass permissions with the decisions check enabled
    And a decisions endpoint that classifies every command as unsafe
    And a model that runs the shell command once, then answers
    When the user asks a question
    Then the tool call is answered with the unsafe rejection
    And the command output is not in the session
    And the turn ends with the model's answer

  Scenario: An unsafe remote command is rejected in bypass mode
    Given a coddy session in agent mode under bypass permissions with the decisions check enabled
    And a decisions endpoint that classifies every command as unsafe
    And a model that runs a remote command over SSH once, then answers
    When the user asks a question
    Then the tool call is answered with the unsafe rejection
    And the decisions endpoint was asked about the command on its remote host
    And the turn ends with the model's answer

  Scenario: A safe command runs
    Given a coddy session in agent mode under bypass permissions with the decisions check enabled
    And a decisions endpoint that classifies every command as safe
    And a model that runs the shell command once, then answers
    When the user asks a question
    Then the command output is in the session
    And the turn ends with the model's answer

  Scenario: A command the operator approved in a prompt is not checked again
    Given a coddy session in agent mode under ask permissions with the decisions check enabled
    And a decisions endpoint that classifies every command as unsafe
    And a model that runs the shell command once, then answers
    When the user asks a question
    Then the command output is in the session
    And the decisions endpoint was not asked

  Scenario: A check without a credential stops the command
    Given a coddy session in agent mode under bypass permissions with the decisions check enabled
    And a decisions endpoint without a credential
    And a model that runs the shell command once, then answers
    When the user asks a question
    Then the tool call is answered with the not-executed refusal
    And the command output is not in the session
    And the turn ends with the model's answer
