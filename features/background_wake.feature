Feature: A finished background task can wake the agent
  A long job is only useful unattended if something restarts the conversation when it ends.
  A task the model marked with notify_on_finish therefore starts a new agent turn on its own,
  carrying the outcome, so the model can end its turn the moment the work is handed off. The
  opt-in is the point: the model decides which results are worth a turn, so a batch of quick
  commands cannot each spend one behind the operator's back.

  Scenario: A task that asked to be notified starts a turn when it finishes
    Given a session with no woken turns
    When a background task that asked to be notified finishes as "succeeded"
    Then the agent is woken once
    And the woken turn names that task and its outcome

  Scenario: A task that did not ask stays quiet
    Given a session with no woken turns
    When a background task that did not ask to be notified finishes as "succeeded"
    Then the agent is not woken

  Scenario: A failure wakes the agent and is reported as a failure
    Given a session with no woken turns
    When a background task that asked to be notified finishes as "failed"
    Then the agent is woken once
    And the woken turn tells the model the work did not succeed

  Scenario: Tasks finishing together cost one turn, not several
    Given a session with no woken turns
    When three background tasks that asked to be notified finish together
    Then the agent is woken once
    And the woken turn names all three tasks

  Scenario: A task that finishes while its own turn is still running is woken when that turn ends
    Given a session with no woken turns
    And an agent turn is already in flight for that session
    When a background task that asked to be notified finishes as "failed"
    And the turn in flight ends
    Then the agent is woken once
    And the woken turn tells the model the work did not succeed

  @real-shell
  Scenario: A task killed by its hard timeout wakes the agent
    Given a session with no woken turns
    When a notifying background task outlives its hard timeout
    Then the agent is woken once
    And the woken turn tells the model the work did not succeed

  @real-shell
  Scenario: A submodule clone refused by SSH wakes the agent with the failure
    Given a session with no woken turns
    And a checkout whose submodule remotes refuse the SSH key
    And an agent turn is already in flight for that session
    When the agent updates its submodules as a notifying background task
    And the turn in flight ends
    Then the agent is woken once
    And the woken turn tells the model the work did not succeed
    And the task output names the clone the remote refused
