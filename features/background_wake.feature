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
