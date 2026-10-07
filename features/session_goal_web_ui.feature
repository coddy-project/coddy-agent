Feature: The web UI shows the session goal and lets the operator steer it
  A goal chip in the composer shows where the supervisor stands; it opens the
  goal popover with everything the supervisor knows and the actions. A bare
  /goal opens the same popover and sends nothing. The turns the supervisor
  starts are goal rows, never user bubbles, live and after a reload, and they
  keep the indices the server knows the messages by.

  Scenario: The goal of the session shows on the chip and its turns as goal rows
    Then the goal of the session read shows on the chip and its turns as goal rows
    And the chip opens the popover with everything the supervisor knows

  Scenario: The operator pauses, resumes and clears the goal
    Then Pause reaches the goal route and the answer moves the chip
    And Resume sends /goal resume as a prompt and leaves the draft alone
    And Clear asks first, then reaches the goal route and the chip goes

  Scenario: A bare /goal opens the popover
    Then a bare /goal opens the goal popover and sends nothing

  Scenario: Goal turns are rows, live and after a reload
    Then on the stream the kickoff row replaces the prompt and each continuation opens a turn of its own
    And after a reload a goal turn is a goal row that opens a turn of its own
    And a prompt typed after a goal turn keeps the index the server knows it by

  Scenario: Every client follows the goal
    Then the events stream moves the chip and an older or another session's snapshot does not
