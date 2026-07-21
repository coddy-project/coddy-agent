Feature: Workspace switching
  Coddy UI sessions can switch between workspace folders, git branches,
  and dedicated worktrees, mirroring the Claude Desktop composer chips.

  Background:
    Given a running coddy HTTP server

  Scenario: Workspace context for a plain folder
    Given a workspace folder "plain" without git
    And a session rooted at folder "plain"
    When I request the workspace context
    Then the context path points to folder "plain"
    And the context reports it is not a git repository

  Scenario: Workspace context inside a git repository
    Given a workspace git repository "repo" with branches "main, feature/login"
    And a session rooted at folder "repo"
    When I request the workspace context
    Then the context reports a git repository on branch "main"
    And the context lists branches "main, feature/login"

  Scenario: Switch the session to another folder
    Given a workspace folder "alpha" without git
    And a workspace folder "beta" without git
    And a session rooted at folder "alpha"
    When I switch the session workspace to folder "beta"
    Then the context path points to folder "beta"
    And the session cwd is persisted as folder "beta"

  Scenario: Check out another branch in place
    Given a workspace git repository "repo" with branches "main, feature/login"
    And a session rooted at folder "repo"
    When I switch the session to branch "feature/login"
    Then the context reports a git repository on branch "feature/login"
    And the context reports the session is not in a worktree

  Scenario: Open a branch in a dedicated worktree
    Given a workspace git repository "repo" with branches "main, feature/login"
    And a session rooted at folder "repo"
    When I switch the session to branch "feature/login" in a worktree
    Then the context reports a git repository on branch "feature/login"
    And the context reports the session is in a worktree
    And the worktree path differs from the repository root

  Scenario: Returning to the default branch leaves the worktree
    Given a workspace git repository "repo" with branches "main, feature/login"
    And a session rooted at folder "repo"
    And the session switched to branch "feature/login" in a worktree
    When I switch the session to branch "main"
    Then the context reports a git repository on branch "main"
    And the context reports the session is not in a worktree

  Scenario: Workspace is locked once the conversation starts
    Given a workspace folder "alpha" without git
    And a workspace folder "beta" without git
    And a session rooted at folder "alpha"
    And the session already has a user message
    When I switch the session workspace to folder "beta"
    Then the workspace request fails with status 409
    And the context path points to folder "alpha"

  Scenario: Browse folders for the workspace picker
    Given a workspace folder "parent" containing subfolders "one, two"
    When I browse workspace folders under "parent"
    Then the folder listing contains "one, two"

  Scenario: Reject switching to a missing folder
    Given a workspace folder "plain" without git
    And a session rooted at folder "plain"
    When I switch the session workspace to a folder that does not exist
    Then the workspace request fails with status 400

  Scenario: Reject branch switching outside a git repository
    Given a workspace folder "plain" without git
    And a session rooted at folder "plain"
    When I switch the session to branch "main"
    Then the workspace request fails with status 400
