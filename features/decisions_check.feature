Feature: Decisions command safety check
  With the check on, a shell command - run_command, or the remote command of
  ssh_run_command - that the operator did not allow explicitly is asked about
  on the NeuralDeep decisions endpoint before the permission gate decides. A
  safe command runs without a prompt. An unsafe one is asked about in the ask
  and accept_edits modes, with the verdict in the prompt, and rejected in
  bypass mode, where nobody would be asked: the refusal says the command was
  classified as dangerous and is the call's tool result, so the session
  records why the command never ran and the model can choose another path. A
  command the allowlist, a session grant or a hook's allow approves is trusted
  and not checked.

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

  Scenario: A safe command runs in bypass mode
    Given a coddy session in agent mode under bypass permissions with the decisions check enabled
    And a decisions endpoint that classifies every command as safe
    And a model that runs the shell command once, then answers
    When the user asks a question
    Then the command output is in the session
    And the turn ends with the model's answer

  Scenario: A safe command runs without a prompt in ask mode
    Given a coddy session in agent mode under ask permissions with the decisions check enabled
    And a decisions endpoint that classifies every command as safe
    And a model that runs the shell command once, then answers
    When the user asks a question
    Then the command output is in the session
    And nobody was asked to approve the command

  Scenario: An unsafe command is asked about in ask mode
    Given a coddy session in agent mode under ask permissions with the decisions check enabled
    And a decisions endpoint that classifies every command as unsafe
    And a model that runs the shell command once, then answers
    When the user asks a question
    Then the operator was asked to approve the command as one classified dangerous
    And the command output is in the session

  Scenario: An allowlisted command is not checked
    Given a coddy session in agent mode under ask permissions with the decisions check enabled
    And the command is on the command allowlist
    And a decisions endpoint that classifies every command as unsafe
    And a model that runs the shell command once, then answers
    When the user asks a question
    Then the command output is in the session
    And the decisions endpoint was not asked
    And nobody was asked to approve the command
