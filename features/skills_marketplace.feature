Feature: Skill marketplace versioning and management
  Coddy installs skills from agents-standard marketplaces, records the
  installed version, detects when a newer version is published, and lets
  operators manage marketplace sources over the HTTP API.

  Background:
    Given a running coddy HTTP server

  Scenario: Install a skill and record its marketplace version
    Given a local marketplace "shop" publishing skill "demo" at version "1.0.0"
    When I add the marketplace "shop" as a skill source and sync
    Then the skills list shows "demo" at version "1.0.0"
    And skill "demo" reports no update available

  Scenario: Detect an available update after a new version is published
    Given a local marketplace "shop" publishing skill "demo" at version "1.0.0"
    And I have added and synced the marketplace "shop"
    When the marketplace "shop" republishes skill "demo" at version "2.0.0"
    And I check for skill updates
    Then skill "demo" reports an update available to version "2.0.0"
    And the skills list still shows "demo" at version "1.0.0"

  Scenario: Updating a skill installs the newer version
    Given a local marketplace "shop" publishing skill "demo" at version "1.0.0"
    And I have added and synced the marketplace "shop"
    And the marketplace "shop" republishes skill "demo" at version "2.0.0"
    When I update the skill "demo"
    Then the skills list shows "demo" at version "2.0.0"
    And skill "demo" reports no update available

  Scenario: List and remove marketplace sources
    Given a local marketplace "shop" publishing skill "demo" at version "1.0.0"
    And I have added and synced the marketplace "shop"
    When I list the skill sources
    Then the source list contains the marketplace "shop"
    When I remove the marketplace "shop" from the skill sources
    Then the source list is empty
