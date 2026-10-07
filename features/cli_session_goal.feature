Feature: Session goal on the console
  The console names the session goal in its footer, in the colour of its
  status, and shows a turn the supervisor starts as a goal row in the
  transcript instead of the instruction the model reads. /goal with an
  objective goes to the session manager, which sets the goal and starts
  working on it at once. A bare /goal opens the goal menu: what the supervisor
  found and the actions the status allows - pause a goal it works on, resume
  one it holds, clear it after a confirmation. A session entered again shows
  its goal right away.

  Scenario: The operator sets a goal, follows it and manages it from the goal menu
    Given a coddy console app whose supervisor finds "the tests still fail"
    When the console app starts
    And the operator sets the goal "make the tests pass"
    Then the transcript shows the goal row "Goal set: make the tests pass"
    And the footer shows "◎ goal active 0/10 (/goal)"
    When the agent finishes its turn
    Then the transcript shows the goal row "Goal continuation 1 of 10: the tests still fail"
    And the footer shows "◎ goal active 1/10 (/goal)"
    When the operator opens the goal menu
    Then the goal menu shows "Last check: not met · the tests still fail"
    When the operator picks "Pause" in the goal menu
    Then the footer shows "◎ goal paused (/goal)"
    When the agent finishes its turn
    And the operator opens the goal menu
    And the operator picks "Resume" in the goal menu
    Then the transcript shows the goal row "Goal resumed: make the tests pass"
    When the supervisor next finds that it needs "which database to use?"
    And the agent finishes its turn
    Then the footer shows "◎ goal blocked (/goal)"
    And the transcript shows "Goal blocked: which database to use?"
    When the operator starts a new session
    Then the footer shows no goal
    When the operator resumes the session before
    Then the footer shows "◎ goal blocked (/goal)"
    And the transcript shows the goal row "Goal resumed: make the tests pass"
    When the operator opens the goal menu
    And the operator picks "Clear" in the goal menu
    And the operator confirms "Clear the goal"
    Then the footer shows no goal
    And the transcript shows "Goal cleared: make the tests pass"
