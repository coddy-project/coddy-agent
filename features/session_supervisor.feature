Feature: A session goal continues until its result is verified
  The supervisor evaluates a finished turn and gives bounded follow-up work.

  Scenario: A first answer leaves work unfinished
    Given a session goal to ship a fix
    And the task runner ends its first turn without the fix
    When the session handles the request
    Then the supervisor starts a visible continuation
    And the session goal is complete after the fix
