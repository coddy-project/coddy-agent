Feature: Windows self-update helper
  Windows refuses to replace coddy.exe while that executable is running, so
  `coddy update` stages the new binary and hands the swap to a helper copied
  into the system temporary directory. The helper waits for the original
  process to exit, installs the update, and starts Coddy again.

  Scenario: Installing the staged update once the original Coddy has exited
    Given a staged Coddy update next to the installed executable
    When the update helper installs it
    Then the installed executable is the staged one
    And the helper leaves no staging or backup files behind
    And the helper hands the restart the path of the helper to delete

  Scenario: Installing without a restart still clears the helper away
    Given a staged Coddy update next to the installed executable
    And the update is installed without starting Coddy again
    When the update helper installs it
    Then the installed executable is the staged one
    And the helper hands the cleanup over without starting Coddy

  Scenario: Restarting a release that predates the restart handoff
    Given a staged Coddy update next to the installed executable
    And the staged release does not understand the restart handoff
    When the update helper installs it
    Then the installed executable is the staged one
    And the helper starts Coddy without the handoff

  Scenario: Clearing a helper an earlier update left behind
    Given an update helper left in the system temporary directory
    When Coddy sweeps helpers from earlier updates
    Then the helper left by the earlier update is gone
    And unrelated files in the temporary directory are untouched
