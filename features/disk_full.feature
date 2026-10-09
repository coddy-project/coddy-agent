Feature: A full disk is reported as a full disk
  When the volume that holds the sessions folder has no room left, a session that
  cannot be created or saved says why. The failure keeps its cause, so a
  surface can tell the client the disk is full instead of "session
  unavailable", and the server log names it at error level instead of a
  warning (issue #465).

  Scenario: A new session on a full disk fails with the cause
    Given the volume that holds the sessions folder has no room left
    When a client creates a session
    Then no session is created
    And no half-built session folder is left behind
    And the failure says the disk is full
    And the log carries an error saying there is no space left on the device

  Scenario: Work in a running session is kept in memory when it cannot be saved
    Given a session was created while the volume had room
    And the volume then runs out of room
    When the session is switched to plan mode
    Then the session is in plan mode
    And the log carries an error saying the session was not saved for lack of space
    And the error tells the operator to free disk space
