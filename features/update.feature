Feature: Coddy self-update
  Coddy downloads an official release archive and replaces the executable it is
  running with the binary inside it. The archive also carries the man page and
  the shell completions, and where the installer put them beside the binary the
  update refreshes them too, so a completer never offers commands the binary no
  longer has. Once the update is in, Coddy says what changed: every release
  between the version that was running and the one just installed, with its
  notes, and the GitHub comparison for the whole range.

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

  Scenario: Refreshing the man page and the shell completions installed beside the binary
    Given a newer Coddy release is available
    And the man page and the shell completions of the installed release sit beside the executable
    When Coddy installs the update
    Then the installed executable is the one from the release
    And the man page and the shell completions are the ones from the release
    And Coddy reports that it refreshed the man page and the shell completions

  Scenario: Reporting what changed since the release that was running
    Given a newer Coddy release is available
    And the releases since the installed version carry their notes
    When Coddy installs the update
    Then the installed executable is the one from the release
    And Coddy lists every release since the installed version with its notes
    And Coddy links the full changelog between the two versions on GitHub

  Scenario: Updating quietly from a script
    Given a newer Coddy release is available
    And the releases since the installed version carry their notes
    When Coddy installs the update with --no-notes
    Then the installed executable is the one from the release
    And Coddy prints no release notes

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
