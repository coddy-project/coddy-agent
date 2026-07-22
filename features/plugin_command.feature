Feature: Plugin and marketplace management via the /plugin command
  The built-in /plugin command manages skill plugins and their marketplaces from
  chat, mirroring the `coddy plugin ...` CLI. It runs deterministically without
  an LLM turn and works over the HTTP prompt surface (/v1/responses).

  Background:
    Given a running coddy plugin server
    And a chat session
    And a local marketplace "shop" publishing skill "demo" at version "1.0.0"

  Scenario: Add a marketplace and install its skills from chat
    When I send the plugin prompt "/plugin marketplace add <shop>"
    Then the plugin response mentions "Added marketplace"
    And the plugin response mentions "1 added"
    And the "/plugin" command is part of the transcript

  Scenario: List marketplaces reports validity status
    Given I have added the marketplace "shop" over chat
    When I send the plugin prompt "/plugin marketplace list"
    Then the plugin response mentions "valid marketplace"
    And the plugin response mentions "shop"

  Scenario: List installed plugins shows versions
    Given I have added the marketplace "shop" over chat
    When I send the plugin prompt "/plugin list"
    Then the plugin response mentions "demo@1.0.0"

  Scenario: Remove a marketplace from chat
    Given I have added the marketplace "shop" over chat
    When I send the plugin prompt "/plugin marketplace remove <shop>"
    Then the plugin response mentions "Removed marketplace"
