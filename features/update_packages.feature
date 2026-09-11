Feature: Updating a Coddy installed from a system package
  A Coddy that apt or dnf put on disk is owned by the package database. Replacing
  that file in place would leave the database describing a build that is no longer
  there, so self-update installs the release package instead - and only a process
  that can write to the package database may do it. Homebrew refuses to run under
  sudo at all, so a brew install has no privileged route and always ends at the
  brew command that upgrades it - and which command that is depends on whether
  brew installed a cask or a formula.

  Scenario: Without root privileges Coddy points at the package manager
    Given Coddy was installed from a deb package
    And Coddy runs without root privileges
    When Coddy tries to install the update
    Then Coddy reports that a package manager owns the installation
    And Coddy names the command that upgrades the package
    And the installed executable is left untouched

  Scenario: Root installs the release package instead of replacing the binary
    Given Coddy was installed from a deb package
    And Coddy runs as root
    When Coddy installs the update
    Then Coddy downloads the release package for this platform
    And Coddy hands the package to the system package manager
    And Coddy reports the release it installed
    And Coddy lists every release since the installed version with its notes
    And Coddy links the full changelog between the two versions on GitHub
    And the installed executable is left untouched

  Scenario: A Homebrew cask install is sent back to brew
    Given Coddy was installed by a Homebrew cask
    And Coddy runs as root
    When Coddy tries to install the update
    Then Coddy reports that a package manager owns the installation
    And Coddy tells the user to run "brew upgrade --cask coddy"
    And the installed executable is left untouched

  Scenario: A Homebrew formula install is sent back to brew without the cask flag
    Given Coddy was installed by a Homebrew formula
    And Coddy runs as root
    When Coddy tries to install the update
    Then Coddy reports that a package manager owns the installation
    And Coddy tells the user to run "brew upgrade coddy"
    And the installed executable is left untouched

  Scenario: Root installs the release package on an rpm system
    Given Coddy was installed from an rpm package
    And Coddy runs as root
    When Coddy installs the update
    Then Coddy downloads the release package for this platform
    And Coddy hands the package to the system package manager
    And Coddy reports the release it installed
