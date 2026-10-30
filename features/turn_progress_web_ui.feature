Feature: The progress of a running turn in the web UI
  A long turn has to show that it is alive. For the whole of a running turn the
  line next to the typing dots leads with the turn's own numbers - how long it
  has been running, how many tokens the model has generated in it, how many
  background tasks are running right now - and then says what the agent is
  doing. The numbers come from the server's turn_progress, so a reloaded tab
  shows the same clock instead of starting a new one.

  Scenario: The working line stays closer to the transcript when no checklist is present
    Then without todos the active live line removes 4px above it and adds no room below
    And the active live line eases checklist spacing changes only without reduced motion

  Scenario: Before the first token the line is the clock and the waiting phrase
    Then before the first token the live line shows the turn clock and the waiting phrase alone

  Scenario: The generated tokens join the line once there are any
    Then the live line shows the generated tokens once there are any, shortened past a thousand

  Scenario: Generation speed rides the token count and outlives the turn
    Then the live line shows generation speed immediately after the running token count
    And a completed generation caption follows the answer and is not duplicated by replay

  Scenario: The line of a running turn carries what the server reported
    Then the live line of a running turn carries the server's clock and token count
    And a turn_progress frame reaches the tab on its own clock

  Scenario: Running background tasks are named on the line and open the Tasks panel
    Then the live line names the running background tasks and opens the Tasks panel
    And the live line of a running turn counts the tasks without the memory run
