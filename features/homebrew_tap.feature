Feature: Coddy installs from its own Homebrew tap
  Current Homebrew loads a cask only out of a tap: a cask named by its URL or by
  a file path is refused, and the command the documentation used to give,
  `brew install --cask <URL>`, ends at "No Cask with this name exists" (issue
  269). Every release therefore renders the cask of the two macOS archives it
  has just built and publishes it to the tap coddy-project/homebrew-coddy-agent,
  where `brew install coddy-project/coddy-agent/coddy` finds it by name and
  `brew upgrade` follows the next release. The cask is written for the Homebrew of today:
  completions through their own stanzas, structured flight steps rather than
  Ruby blocks, and the quarantine Homebrew puts on every cask download lifted
  from the binary, which is not notarised.

  Scenario: A release renders the cask of its own macOS archives
    Given the macOS archives of release "1.3.14"
    When the Homebrew cask is rendered for release "1.3.14"
    Then the cask "coddy" is at version "1.3.14"
    And on "arm" it downloads "coddy_1.3.14_darwin_arm64.tar.gz" with that archive's checksum
    And on "intel" it downloads "coddy_1.3.14_darwin_amd64.tar.gz" with that archive's checksum
    And it installs the binary, the man page and the bash and zsh completions
    And it lifts the quarantine from the installed binary
    And it uses no stanza current Homebrew deprecates

  Scenario: A release publishes its cask to the tap
    Given a Homebrew tap whose cask is at release "1.3.13"
    And the macOS archives of release "1.3.14"
    And the Homebrew cask is rendered for release "1.3.14"
    When release "1.3.14" publishes its cask to the tap
    Then the cask in the tap is the one rendered for release "1.3.14"
    And the newest commit of the tap names release "1.3.14"
