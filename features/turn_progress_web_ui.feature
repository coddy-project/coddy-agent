Feature: The progress of a running turn in the web UI
  A long turn has to show that it is alive. For the whole of a running turn the
  line next to the Coddy work indicator leads with the turn's own numbers - how long it
  has been running, how many tokens the model has generated in it, how many
  background tasks are running right now - and then says what the agent is
  doing. The numbers come from the server's turn_progress, so a reloaded tab
  shows the same clock instead of starting a new one.

  Scenario: The Coddy logo indicates work with its rotating outer contour
    Then the working line shows a decorative Coddy logo instead of bouncing dots
    And its outer contour rotates, grows and shrinks, and reduced motion keeps it still

  Scenario: The line stands the same distance from the step it reports on and from what follows
    Then the live line keeps the transcript's rhythm above and below
    And the live line's room does not depend on the checklist
    And the finished line is the same box as the live line it replaces
    And the live line animates no padding of its own

  Scenario: Before the first token the line is the clock and the waiting phrase
    Then before the first token the live line shows the turn clock and the waiting phrase alone

  Scenario: The generated tokens join the line once there are any
    Then the live line shows the generated tokens once there are any, shortened past a thousand

  Scenario: Generation speed rides the token count and outlives the turn
    Then the live line shows generation speed immediately after the running token count
    And a completed generation caption follows the answer and is not duplicated by replay
    And a finished turn keeps the live line without its phrase, the time on hover
    And the live line stands until the turn is released, then simply stops

  Scenario: On a phone the numbers lead the line, so the end of a turn leaves them where they stand
    Then on a phone the turn's numbers keep the first line and the phrase goes under them

  Scenario: The line of a running turn carries what the server reported
    Then the live line of a running turn carries the server's clock and token count
    And a turn_progress frame reaches the tab on its own clock

  Scenario: Running background tasks are named on the line and open the Tasks panel
    Then the live line names the running background tasks and opens the Tasks panel
    And the live line of a running turn counts the tasks without the memory run
