Feature: The web UI shows a woken turn as the agent carrying on
  A turn a finished background task started was typed by nobody. The web UI
  shows nothing in place of its first message - no note and no user bubble -
  so the agent's answer reads as the work carrying on, whether the turn arrives
  on its stream or with the transcript after a reload. The wake still opens a
  turn, so the messages after it keep the indices the server knows them by.
  What woke the agent is on the task's card in the Tasks panel: a bell while
  the task will wake the agent, and after it ended when it did.

  Scenario: A woken turn shows only the agent carrying on
    Then a woken turn shows neither a note nor a user bubble, only the agent carrying on

  Scenario: The wake opens the woken turn on its stream
    Then a background_wake frame opens the woken turn before anything it says

  Scenario: After a reload the wake is read from the transcript
    Then the transcript's wake reads the same as the stream's
    And a wake from the stream and the same wake from the transcript are one row

  Scenario: The wake still counts as a turn
    Then a message typed after a wake is edited by the index the server knows it by
    And a branch after a wake lands on its message

  Scenario: The task's card says it will wake the agent, and that it did
    Then a running task that wakes the agent carries a bell after its title
    And a finished task that woke the agent keeps its bell
