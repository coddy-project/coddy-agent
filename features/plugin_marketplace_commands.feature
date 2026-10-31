Feature: Adding a marketplace, installing its plugins one at a time and updating them
  The plugin command takes the words Claude Code takes. `plugin marketplace
  add <source>` adds a marketplace: Coddy reads the list of plugins its
  marketplace.json publishes and installs none of them. `plugin install
  <plugin>@<marketplace>` installs one plugin of an added marketplace, found by
  the name its marketplace.json gives, and reads that list again first, so a
  plugin published after the marketplace was added installs without an update.
  `plugin marketplace update` reads the list of every marketplace and source
  again and reports the installed skills that have a newer version; it installs
  and updates nothing. `plugin update` installs those newer versions, of every
  installed skill or of the one it names, and never installs a skill that is
  not installed. A source installed with `plugin install <owner/repo | url>`
  has every plugin it publishes installed at that moment.

  Background:
    Given a coddy home without skill sources
    And git is not installed
    And an https marketplace "neuraldeep" publishing the plugins "yandex-wordstat" and "ru-text" as zip archives with their sha256

  Scenario: Adding a marketplace reads its plugins and installs none
    When I run the plugin command "marketplace add <neuraldeep>"
    Then the plugin command answers "Added marketplace"
    And the plugin command answers "2 plugin(s)"
    And the plugin command answers "plugin install <plugin>@neuraldeep"
    And no skill is installed

  Scenario: Adding the same marketplace again refreshes its list
    Given I have run the plugin command "marketplace add <neuraldeep>"
    And the marketplace "neuraldeep" also publishes the plugin "wordstat-pro" as a zip archive
    When I run the plugin command "marketplace add <neuraldeep>"
    Then the plugin command answers "is already added; its list is refreshed: 3 plugin(s)"
    And no skill is installed

  Scenario: Installing one plugin of an added marketplace
    Given I have run the plugin command "marketplace add <neuraldeep>"
    When I run the plugin command "install yandex-wordstat@neuraldeep"
    Then the plugin command answers "Installed yandex-wordstat@neuraldeep. 1 added, 0 updated, 0 failed."
    And the skill "yandex-wordstat" is installed
    And the skill "ru-text" is not installed

  Scenario: A plugin published after the marketplace was added installs without an update
    Given I have run the plugin command "marketplace add <neuraldeep>"
    And the marketplace "neuraldeep" also publishes the plugin "wordstat-pro" as a zip archive
    When I run the plugin command "install wordstat-pro@neuraldeep"
    Then the plugin command answers "1 added, 0 updated, 0 failed."
    And the skill "wordstat-pro" is installed

  Scenario: Updating a marketplace reports the newer versions and installs nothing
    Given I have run the plugin command "marketplace add <neuraldeep>"
    And I have run the plugin command "install yandex-wordstat@neuraldeep"
    And the marketplace "neuraldeep" publishes a new archive of the plugin "yandex-wordstat"
    When I run the plugin command "marketplace update neuraldeep"
    Then the plugin command answers "1 update available"
    And the plugin command answers "yandex-wordstat"
    And the plugin command answers "plugin update"
    And the skill "yandex-wordstat" on disk does not come from the new archive
    And the skill "ru-text" is not installed

  Scenario: plugin update installs every newer version and installs no other plugin
    Given I have run the plugin command "marketplace add <neuraldeep>"
    And I have run the plugin command "install yandex-wordstat@neuraldeep"
    And the marketplace "neuraldeep" publishes a new archive of the plugin "yandex-wordstat"
    When I run the plugin command "update"
    Then the plugin command answers "0 added, 1 updated, 0 failed."
    And the skill "yandex-wordstat" on disk comes from the new archive
    And the skill "ru-text" is not installed

  Scenario: plugin update with a name updates that plugin
    Given I have run the plugin command "marketplace add <neuraldeep>"
    And I have run the plugin command "install yandex-wordstat@neuraldeep"
    And I have run the plugin command "install ru-text@neuraldeep"
    And the marketplace "neuraldeep" publishes a new archive of the plugin "yandex-wordstat"
    And the marketplace "neuraldeep" publishes a new archive of the plugin "ru-text"
    When I run the plugin command "update ru-text"
    Then the plugin command answers "Updated ru-text. 0 added, 1 updated, 0 failed."
    And the skill "ru-text" on disk comes from the new archive
    And the skill "yandex-wordstat" on disk does not come from the new archive

  Scenario: A skill removed by the operator is not installed back by an update
    Given I have run the plugin command "marketplace add <neuraldeep>"
    And I have run the plugin command "install yandex-wordstat@neuraldeep"
    And I have run the plugin command "remove yandex-wordstat"
    And the marketplace "neuraldeep" publishes a new archive of the plugin "yandex-wordstat"
    When I run the plugin command "marketplace update"
    And I run the plugin command "update"
    Then no skill is installed

  Scenario: Installing a source installs every plugin it publishes
    When I run the plugin command "install <neuraldeep>"
    Then the plugin command answers "Installed"
    And the plugin command answers "2 added, 0 updated, 0 failed."
    And the skill "yandex-wordstat" is installed
    And the skill "ru-text" is installed

  Scenario: Updating the marketplaces never installs a source of the home marketplaces.json
    Given the address of the marketplace "neuraldeep" is a source of the home marketplaces.json
    When I run the plugin command "marketplace update"
    Then the plugin command answers "Every installed skill is up to date."
    And no skill is installed
