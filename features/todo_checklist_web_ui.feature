Feature: The todo checklist under the transcript in the web UI
  While a session keeps a todo checklist with work left in it, the newest end
  of the conversation shows the whole list: every item with its status mark,
  the item in progress marked as the one the agent is on. The list comes with
  the transcript read, so a reloaded tab shows it at once, and the plan frames
  of a running turn move it as the agent ticks items off. While a turn runs,
  the mark of the item in progress spins and its text shimmers. The reader can
  fold the list down to the item the agent is on, and every change of state
  moves rather than jumps.

  Scenario: Opening a conversation shows its checklist under the newest message
    Then a conversation whose checklist has work left shows the list under its newest message
    And the item in progress is marked, and its mark stands still while no turn runs

  Scenario: A running turn moves the checklist as the agent works through it
    Then the plan frames of a running turn move the checklist and the mark of the item in progress spins
    And a plan frame of a turn reaches the tab, a design plan's frame does not

  Scenario: A finished list leaves once the turn is over
    Then the checklist stays while the turn that finished it runs and leaves once the turn is over
    And the checklist stands above the live line and only at the newest end of the transcript

  Scenario: The list folds down to the item the agent is on
    Then a click on the list's head folds it to the item in progress and the choice is remembered

  Scenario: The list moves between its states instead of jumping
    Then a status changes the mark in place and a list that stops showing fades out before it leaves
    And every motion of the list is drawn only for a reader who did not ask for less motion
