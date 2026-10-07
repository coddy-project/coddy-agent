Feature: A browser follows the session goal over HTTP
  The web UI sets a goal with /goal, watches the supervisor work on it in the
  turn's stream, reads it back with the session, and pauses or clears it
  through the goal route.

  Scenario: A goal from /goal to its pause and its removal
    Given a Coddy server whose supervisor finds the goal met on its second check
    When the browser sends "/goal ship the fix" as a streamed prompt
    Then the stream carries the goal kickoff and the goal updates
    And the goal route reports the goal complete after one continuation
    And the session messages carry the goal and both goal turns
    When the browser sets the objective "write the docs" through the goal route
    And the browser pauses the goal through the goal route
    Then the goal route reports the goal "write the docs" as paused
    When the browser clears the goal through the goal route
    Then the goal route reports no goal
