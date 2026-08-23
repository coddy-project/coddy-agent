Feature: Coddy self-update
  Coddy downloads an official release archive and replaces the executable it is
  running with the binary inside it.

  Scenario: Installing a newer release
    Given a newer Coddy release is available
    When Coddy installs the update
    Then the installed executable is the one from the release
    And Coddy reports the release it installed

  Scenario: Verifying the download against the published checksums
    Given a newer Coddy release is available
    When Coddy installs the update
    Then Coddy reports that it verified the archive against the published checksums

  Scenario: Resuming a download the server cut short
    Given a newer Coddy release is available
    And the download server drops the first connection halfway
    When Coddy installs the update
    Then the installed executable is the one from the release
    And Coddy reports that it resumed the download

  Scenario: Scheduling a downloaded Windows update
    Given a newer Windows Coddy release is available
    When Coddy prepares the Windows update
    Then it reports that the update is ready
    And it schedules a helper that will restart Coddy

  Scenario: Installing a Windows update without starting Coddy again
    Given a newer Windows Coddy release is available
    When Coddy prepares the Windows update with --no-restart
    Then it reports that the update is ready
    And it schedules a helper that will leave Coddy stopped
