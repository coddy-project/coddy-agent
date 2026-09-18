Feature: The progress of a running turn in the web UI
  A long turn has to show that it is alive. For the whole of a running turn the
  line next to the typing dots leads with the turn's own numbers - how long it
  has been running, how many tokens the model has generated in it, how many
  background tasks are running right now - and then says what the agent is
  doing. The numbers come from the server's turn_progress, so a reloaded tab
  shows the same clock instead of starting a new one.

  Scenario: Before the first token the line is the clock and the waiting phrase
    Then before the first token the live line shows the turn clock and the waiting phrase alone

  Scenario: The generated tokens join the line once there are any
    Then the live line shows the generated tokens once there are any, shortened past a thousand

  Scenario: The line of a running turn carries what the server reported
    Then the live line of a running turn carries the server's clock and token count
    And a turn_progress frame reaches the tab on its own clock

  Scenario: Running background tasks are named on the line and open the Tasks panel
    Then the live line names the running background tasks and opens the Tasks panel
    And with a turn and tasks both running the chip under the transcript steps aside
