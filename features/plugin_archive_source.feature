Feature: Plugins published as zip archives
  A marketplace can publish a plugin as a zip archive instead of a git
  repository. Its entry names the archive the way Claude Code reads it,
  {"source": "archive", "url": "https://...", "sha256": "..."}, the sha256
  being optional. Coddy downloads the archive over https, unpacks it, takes
  the plugin root from the top of the archive or from the one folder that
  wraps it, and installs the skills its plugin manifest names: the ones
  "skills" in .claude-plugin/plugin.json declares, else the folders under
  skills/, else the SKILL.md at the plugin root, for a plugin that is one
  skill. An archive without a manifest is searched for every SKILL.md, as a
  cloned repository is. No git process is started for it, and the plugin
  command touches only the marketplace it names.

  Background:
    Given a coddy home that also names a git marketplace
    And git is not installed

  Scenario Outline: plugin install puts the skill of an archive plugin in place
    Given an https marketplace "catalog" publishing the plugin "demo" as a zip archive <layout>
    When I run the plugin command "install" for the marketplace "catalog"
    Then the plugin command answers "1 added, 0 updated, 0 failed."
    And the skill "demo" is installed with its executable script "scripts/run.sh"
    And the lock records the skill "demo" from the marketplace "catalog" at the version of its archive

    Examples:
      | layout                      |
      | with the plugin at its root |
      | wrapped in one folder       |

  Scenario: From an author's plugin only the skills of its manifest are installed
    Given an https marketplace "catalog" publishing the plugin "ru-text" as a zip archive of its author's repository with a SKILL.md in its test data
    When I run the plugin command "install" for the marketplace "catalog"
    Then the plugin command answers "1 added, 0 updated, 0 failed."
    And the skill "ru-text" is installed with its executable script "scripts/run.sh"
    And the skill "corpus" is not installed

  Scenario: A plugin that is one skill at its root installs that skill
    Given an https marketplace "neuraldeep" publishing the plugin "logika" as a zip archive with its skill at the plugin root
    And I have run the plugin command "marketplace add <neuraldeep>"
    When I run the plugin command "install logika@neuraldeep"
    Then the plugin command answers "Installed logika@neuraldeep. 1 added, 0 updated, 0 failed."
    And the skill "logika" is installed with its executable script "scripts/run.sh"

  Scenario: A new archive of the plugin is offered as an update and installed by plugin update
    Given an https marketplace "catalog" publishing the plugin "demo" as a zip archive with its sha256
    And I have run the plugin command "install" for the marketplace "catalog"
    When the marketplace "catalog" publishes a new archive of the plugin "demo"
    Then the update check offers the skill "demo" at the version of the new archive
    When I run the plugin command "update demo"
    Then the plugin command answers "0 added, 1 updated, 0 failed."
    And the skill "demo" on disk comes from the new archive
    And the lock records the skill "demo" from the marketplace "catalog" at the version of its archive
    And the update check offers no update for the skill "demo"
