Feature: The subagent catalog lists definitions from every scope
  An operator wants to see which subagents a workspace offers before the model uses them:
  the built-ins, their own files under the coddy home, and the files the project brought
  along, together with whether a project file has been approved for this workspace.

  Scenario: Project, user and built-in definitions are listed with their scopes
    Given a coddy home with a subagent definition "notes-taker"
    And a workspace with a subagent definition "reviewer" under .coddy/agents
    When I list the subagent catalog for that workspace
    Then the listing names "explore" as a built-in
    And the listing names "notes-taker" with scope "user"
    And the listing names "reviewer" with scope "project" and trust "needs_approval"

  Scenario: A project definition wins over a user definition with the same name
    Given a coddy home with a subagent definition "reviewer" described as "user copy"
    And a workspace with a subagent definition "reviewer" under .coddy/agents described as "project copy"
    When I list the subagent catalog for that workspace
    Then the listing describes "reviewer" as "project copy"

  Scenario: Trusting a project definition is recorded for that workspace only
    Given a workspace with a subagent definition "reviewer" under .coddy/agents
    When I trust the subagent "reviewer" for that workspace
    And I list the subagent catalog for that workspace
    Then the listing names "reviewer" with scope "project" and trust "trusted"
    When I list the subagent catalog for a different workspace holding the same file
    Then the listing names "reviewer" with scope "project" and trust "needs_approval"
