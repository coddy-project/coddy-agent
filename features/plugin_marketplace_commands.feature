Feature: Adding a marketplace and installing its plugins one at a time
  The plugin command takes the words Claude Code takes. `plugin marketplace
  add <source>` adds a marketplace: Coddy reads the list of plugins its
  marketplace.json publishes and installs none of them. `plugin install
  <plugin>@<marketplace>` installs one plugin of an added marketplace, found by
  the name its marketplace.json gives, and reads that list again first, so a
  plugin published after the marketplace was added installs without an update.
  `plugin marketplace update <marketplace>` refreshes the list and the plugins
  installed from the marketplace, and installs no other. A source installed
  with `plugin install <owner/repo | url>`, and every source the home
  marketplaces.json declares, keeps having every plugin it publishes installed.

  Background:
    Given a coddy home without skill sources
    And git is not installed
    And an https marketplace "neuraldeep" publishing the plugins "yandex-wordstat" and "ru-text" as zip archives

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

  Scenario: Updating a marketplace updates the plugins installed from it and no other
    Given I have run the plugin command "marketplace add <neuraldeep>"
    And I have run the plugin command "install yandex-wordstat@neuraldeep"
    And the marketplace "neuraldeep" publishes a new archive of the plugin "yandex-wordstat"
    When I run the plugin command "marketplace update neuraldeep"
    Then the plugin command answers "0 added, 1 updated, 0 failed."
    And the skill "yandex-wordstat" on disk comes from the new archive
    And the skill "ru-text" is not installed

  Scenario: A source of the home marketplaces.json has every plugin it publishes installed
    Given the address of the marketplace "neuraldeep" is a source of the home marketplaces.json
    When I run the plugin command "marketplace sync"
    Then the plugin command answers "2 added, 0 updated, 0 failed."
    And the skill "yandex-wordstat" is installed
    And the skill "ru-text" is installed
