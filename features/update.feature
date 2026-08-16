Feature: Windows self-update
  Coddy must be able to replace its own executable after the current process exits.

  Scenario: Scheduling a downloaded Windows update
    Given a newer Windows Coddy release is available
    When Coddy prepares the Windows update
    Then it reports that the update is ready
    And it schedules a helper that will restart Coddy
