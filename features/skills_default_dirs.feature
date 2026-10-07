Feature: Default skill directories that every workspace reads
  Four folders are read in every workspace whatever the configuration says,
  lowest priority first: the user's ~/.agents/skills, the project's
  .agents/skills, Coddy's own ${CODDY_HOME}/skills and the project's
  .coddy/skills. A name found in several is taken from the last of them.
  skills.dirs only adds directories after the four, and those win a name over
  the defaults. The project folders follow the workspace of the session, and
  before a session exists the folder picked on the start screen.

  Background:
    Given project folders "app" and "ops"

  Scenario: The project's agents skills are read with no configuration at all
    Given the project folder "app" has the skill "agents-app" in ".agents/skills" described "project agents"
    And a running coddy HTTP server with no skill directories configured
    When I list slash commands without a session for the picked folder "app"
    Then the slash commands include "agents-app"
    Given a session anchored on the project folder "app"
    When I list slash commands for that session
    Then the slash commands include "agents-app"

  Scenario: Switching the session to another folder replaces the skills of the default folders
    Given the project folder "app" has the skill "agents-app" in ".agents/skills" described "project agents"
    And the project folder "ops" has the skill "coddy-ops" in ".coddy/skills" described "project coddy"
    And a running coddy HTTP server with no skill directories configured
    And a session anchored on the project folder "app"
    When I switch that session workspace to the project folder "ops"
    And I list slash commands for that session
    Then the slash commands include "coddy-ops"
    And the slash commands do not include "agents-app"

  Scenario: Coddy's own skills override the project's agents skills
    Given the project folder "app" has the skill "chain" in ".agents/skills" described "project agents"
    And Coddy's own skills folder has the skill "chain" described "coddy home"
    And a running coddy HTTP server with no skill directories configured
    When I list slash commands without a session for the picked folder "app"
    Then the slash command "chain" is described "coddy home"

  Scenario: The project's Coddy skills override Coddy's own
    Given Coddy's own skills folder has the skill "chain" described "coddy home"
    And the project folder "app" has the skill "chain" in ".coddy/skills" described "project coddy"
    And a running coddy HTTP server with no skill directories configured
    When I list slash commands without a session for the picked folder "app"
    Then the slash command "chain" is described "project coddy"

  Scenario: A directory of skills.dirs comes after the defaults and wins a name
    Given the project folder "app" has the skill "chain" in ".coddy/skills" described "project coddy"
    And the project folder "app" has the skill "agents-app" in ".agents/skills" described "project agents"
    And the extra directory "team" has the skill "chain" described "team"
    And a running coddy HTTP server with the extra skill directory "team"
    When I list slash commands without a session for the picked folder "app"
    Then the slash command "chain" is described "team"
    And the slash commands include "agents-app"

  Scenario: The project's .agents/skills may be a link to a folder outside the project
    Given the project folder "ops" links ".agents/skills" to a folder outside it with the skill "shared-agents"
    And a running coddy HTTP server with no skill directories configured
    When I list slash commands without a session for the picked folder "ops"
    Then the slash commands include "shared-agents"
