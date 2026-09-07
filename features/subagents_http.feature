Feature: The HTTP surface exposes subagent runs and definitions
  A subagent run is a background task of the parent session, so the tasks drawer already
  polls it. The REST surface tells the drawer that a task is an agent and which child
  session holds its transcript, keeps child sessions out of the History list unless asked,
  lists the definitions a workspace offers with their trust state, and removes a whole
  session tree when the parent goes.

  Scenario: An agent task row names its child session
    Given a running coddy http server with a session
    And that session started a subagent task for "reviewer" backed by child session "sub_bdd_child"
    When I GET the background tasks of that session
    Then the response lists a task of kind "agent"
    And that task row names the agent "reviewer" and the child session "sub_bdd_child"

  Scenario: Child sessions stay out of the History list unless asked
    Given a running coddy http server with a session
    And a persisted child session "sub_bdd_hidden" spawned by that session
    When I GET the sessions list
    Then the sessions list does not include "sub_bdd_hidden"
    When I GET the sessions list with include_subagents
    Then the sessions list includes "sub_bdd_hidden"

  Scenario: A child transcript is readable while the child runs and after it finished
    Given a running coddy http server with a session
    And a live child session "sub_bdd_live" of that session whose transcript says "child progress"
    When I GET the messages of "sub_bdd_live"
    Then the messages contain "child progress"
    When the child session "sub_bdd_live" is retired
    And I GET the messages of "sub_bdd_live"
    Then the messages contain "child progress"

  Scenario: The catalog lists built-ins and project definitions with their trust state
    Given a running coddy http server with a session
    And the server workspace has a subagent definition "reviewer" under .coddy/agents
    When I GET the subagent catalog for the server workspace
    Then the catalog names the built-in "explore"
    And the catalog names "reviewer" with scope "project" needing approval
    When I POST trust for the subagent "reviewer" in the server workspace
    And I GET the subagent catalog for the server workspace
    Then the catalog names "reviewer" as trusted

  Scenario: Deleting a running child stops its task first
    Given a running coddy http server with a session
    And a live child session "sub_bdd_running" of that session backed by a running subagent task
    When I DELETE the session "sub_bdd_running"
    Then the subagent task of "sub_bdd_running" is no longer running
    And the session bundle "sub_bdd_running" is gone

  Scenario: Deleting a parent removes a nested descendant that is still running
    Given a running coddy http server with a session
    And a live child session "sub_bdd_mid" of that session backed by a running subagent task
    And a live child session "sub_bdd_leaf" of "sub_bdd_mid" backed by a running subagent task
    When I DELETE the parent session
    Then the subagent task of "sub_bdd_leaf" is no longer running
    And the session bundles "sub_bdd_mid" and "sub_bdd_leaf" are gone
