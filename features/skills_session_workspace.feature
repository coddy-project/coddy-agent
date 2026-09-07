Feature: Project-local skills follow the session workspace
  One coddy HTTP server serves sessions rooted in different workspaces. A
  skills.dirs entry written with ${CWD} names the workspace of the session
  that asks, not the directory the server process was started from, and the
  settings API reports the entry exactly as it was configured.

  Background:
    Given skills are configured under "${CWD}/.agents/skills"
    And a project folder "app" with a local skill "deploy-app"
    And a running coddy HTTP server started outside the project

  Scenario: Slash commands of a session anchored on the project list its local skill
    Given a session anchored on the project folder "app"
    When I list slash commands for that session
    Then the slash commands include "deploy-app"

  Scenario: Slash commands without a session use the server default workspace
    When I list slash commands without a session
    Then the slash commands do not include "deploy-app"

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
