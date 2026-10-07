Feature: Skill marketplaces declared by the operator and by a project
  Skill marketplaces are declared in two files of one shape: the operator's
  ~/.coddy/marketplaces.json and the project's .coddy/marketplaces.json. A
  source is installed whole, a marketplace is a catalog whose plugins are
  installed one by one. The project file arrives with the checkout, so under
  skills.project_trust: ask (the default) its entries stay out of every sync
  until the operator approves that exact entry for that workspace, and
  whatever they install goes to the skills folder of the coddy home.
  config.yaml declares none: an old skills.sources list is moved into the
  operator's file when the configuration loads.

  Background:
    Given a coddy home without skill sources
    And git is not installed
    And an https marketplace "team" publishing the plugins "lint-rules" and "review-notes" as zip archives

  Scenario: A project source syncs only once the operator approves it
    Given the workspace's marketplaces.json declares the marketplace "team" as a source
    When I run the plugin command "marketplace sync"
    Then the plugin command answers "0 added, 0 updated, 0 failed."
    And the plugin command answers "not approved for this workspace"
    And no skill is installed
    When I run the plugin command "marketplace trust <team>" in a terminal
    Then the plugin command answers "Approved source"
    When I run the plugin command "marketplace sync"
    Then the plugin command answers "2 added, 0 updated, 0 failed."
    And the skill "lint-rules" is installed
    And the skill "review-notes" is installed

  Scenario: A source the operator adds to the project is approved by writing it
    When the operator adds the marketplace "team" to the workspace's marketplaces.json
    And I run the plugin command "marketplace sync"
    Then the plugin command answers "2 added, 0 updated, 0 failed."
    And the skill "lint-rules" is installed

  Scenario: An old config hands its skills.sources to the home marketplaces.json
    Given a config.yaml whose skills.sources names the marketplace "team"
    When coddy loads the configuration
    Then the home marketplaces.json declares the marketplace "team" as a source
    And config.yaml no longer has skills.sources
    When I run the plugin command "marketplace sync"
    Then the plugin command answers "2 added, 0 updated, 0 failed."
