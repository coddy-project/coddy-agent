Feature: Project-local skills follow the session workspace
  One coddy HTTP server serves sessions rooted in different workspaces. A
  skills.dirs entry written with ${CWD} names the workspace of the session
  that asks, not the directory the server process was started from, and the
  settings API reports the entry exactly as it was configured. Before the
  first message a new chat has no session yet: the folder picked on the
  start screen travels as the cwd query parameter, so the composer lists the
  skills of that folder and not those of the server default workspace, and
  an @agent: mention offers the subagents that folder defines.

  Background:
    Given skills are configured under "${CWD}/.agents/skills"
    And a project folder "app" with a local skill "deploy-app"
    And a project folder "ops" with a local skill "deploy-ops"
    And a running coddy HTTP server started outside the project

  Scenario: Slash commands of a session anchored on the project list its local skill
    Given a session anchored on the project folder "app"
    When I list slash commands for that session
    Then the slash commands include "deploy-app"

  Scenario: Switching workspace immediately replaces the session local skills
    Given a session anchored on the project folder "app"
    When I switch that session workspace to the project folder "ops"
    And I list slash commands for that session
    Then the slash commands include "deploy-ops"
    And the slash commands do not include "deploy-app"

  Scenario: Slash commands without a session use the server default workspace
    When I list slash commands without a session
    Then the slash commands do not include "deploy-app"

  Scenario: Slash commands of a new chat follow the folder picked before the session exists
    When I list slash commands without a session for the picked folder "app"
    Then the slash commands include "deploy-app"
    And the slash commands do not include "deploy-ops"

  Scenario: Picking another folder before the session exists replaces the local skills
    When I list slash commands without a session for the picked folder "app"
    And I list slash commands without a session for the picked folder "ops"
    Then the slash commands include "deploy-ops"
    And the slash commands do not include "deploy-app"

  Scenario: The skills list of a new chat follows the folder picked before the session exists
    When I list skills without a session for the picked folder "app"
    Then the skills list includes "deploy-app" from the project folder "app"

  Scenario: An @agent: mention of a new chat offers the subagents of the picked folder
    Given the project folder "app" has a local subagent "app-reviewer"
    When I search the mentions "agent:" without a session for the picked folder "app"
    Then the mention candidates include "@agent:app-reviewer"
    When I search the mentions "agent:" without a session for the picked folder "ops"
    Then the mention candidates do not include "@agent:app-reviewer"

  Scenario: A project skill linked from outside the project is listed and loaded
    Given the project folder "app" links the skill "shared-app" from outside the project
    And a session anchored on the project folder "app"
    When I list slash commands for that session
    Then the slash commands include "shared-app"
    When I prompt that session with "/shared-app go"
    Then the turn runs with the skill "shared-app" loaded

  Scenario: Deleting a linked project skill removes the link and keeps what it points at
    Given the project folder "app" links the skill "shared-app" from outside the project
    And a session anchored on the project folder "app"
    When I delete the skill "shared-app" for that session
    Then the skill link "shared-app" is gone from the project folder "app"
    And the linked skill "shared-app" is still on disk outside the project

  Scenario: The workspace of an existing session wins over a picked folder
    Given a session anchored on the project folder "app"
    When I list slash commands for that session with the picked folder "ops"
    Then the slash commands include "deploy-app"
    And the slash commands do not include "deploy-ops"

  Scenario: The skills list of a session anchored on the project includes its local skill
    Given a session anchored on the project folder "app"
    When I list skills for that session
    Then the skills list includes "deploy-app" from the project folder "app"

  Scenario: An agent turn on the anchored session runs with the project skill loaded
    Given a session anchored on the project folder "app"
    When I prompt that session with "/deploy-app ship it"
    Then the turn runs with the skill "deploy-app" loaded

  Scenario: The settings API reports the configured entry verbatim
    When I read the server configuration
    Then the skills directories include "${CWD}/.agents/skills"
