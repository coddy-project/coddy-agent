Feature: The background tasks of a chat in the web UI
  Background tasks belong to one chat, so the way to their panel lives in that
  chat: a control at the right edge of the chat header. The header is sticky,
  so the control does not scroll away with the transcript, and it is there
  from the first message, so the header does not jump when the first task
  starts. It says how many tasks run out of how many the chat has.

  Scenario: The control is there before any task has run
    Then the tasks control is in the header of a chat that never ran a task, without counts

  Scenario: The control counts the running tasks out of all of them
    Then with tasks the header control says how many are running out of how many there are
    And once everything has finished the header control keeps the total and drops the live mark

  Scenario: The control opens the Tasks panel and puts it away again
    Then the header control opens the Tasks panel and a second click closes it

  Scenario: Nothing is left under the transcript
    Then the transcript ends with the conversation and the header control is the way to the tasks

  Scenario: Every task is the same card
    Then running and finished tasks are the same card with a dot, a tag, a title and a meta line
    And a card names what runs in a tag on the left and the work in its title

  Scenario: A card opens in place
    Then a click on a card expands it in place and another folds it
    And an open command card shows the command with a copy control, the output and how it ended
    And an open subagent card offers the child transcript and shows the run's log

  Scenario: Stop stays its own control
    Then Stop on a running card stops the task without opening the card
