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
    And the context names "main" as the default branch
    And the session list names "repo" as its main checkout

  Scenario: The dedicated worktree lives inside the repository
    Given a workspace git repository "repo" with branches "main, feature/login"
    And a session rooted at folder "repo"
    When I switch the session to branch "feature/login" in a worktree
    Then the worktree path is ".coddy/worktrees/feature-login" inside repository "repo"
    And repository "repo" reports no untracked files

  Scenario: Returning to the default branch leaves the worktree
    Given a workspace git repository "repo" with branches "main, feature/login"
    And a session rooted at folder "repo"
    And the session switched to branch "feature/login" in a worktree
    When I switch the session to branch "main"
    Then the context reports a git repository on branch "main"
    And the context reports the session is not in a worktree

  Scenario: A removed managed worktree falls back to its parent checkout
    Given a workspace git repository "repo" with branches "main, feature/login"
    And a session rooted at folder "repo"
    And the session switched to branch "feature/login" in a worktree
    When Git removes the session worktree
    And I reopen the session transcript
    Then the context path points to folder "repo"
    And the session list names "repo" as its main checkout
    And the persisted session cwd remains the removed worktree

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

  Scenario: Create a folder from the workspace picker
    Given a workspace folder "parent" containing subfolders "one, two"
    When I create the folder "three" under "parent"
    Then the folder listing points at "three" inside "parent"
    When I browse workspace folders under "parent"
    Then the folder listing contains "one, three, two"

  Scenario: Browse the machine drives for the workspace picker
    Given the host reports drives "X:\, Y:\"
    When I browse the workspace drive list
    Then the folder listing contains "X:, Y:"
    And the folder listing has no folder above it

  Scenario: Walk up from a filesystem root to the drive list
    Given the host reports drives "X:\, Y:\"
    When I browse workspace folders under the filesystem root
    Then the folder listing offers the drive list above it

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

  Scenario: Switching the session workspace defers its configured MCP servers
    Given MCP project trust is "allow"
    And a workspace folder "alpha" without git
    And a workspace folder "beta" without git
    And folder "alpha" declares the project MCP server "alpha-probe"
    And folder "beta" declares the project MCP server "beta-probe"
    And a session rooted at folder "alpha"
    When I switch the session workspace to folder "beta"
    Then the context path points to folder "beta"
    And the session's configured MCP clients are "alpha-probe"

  Scenario: An in-place branch checkout defers workspace MCP servers
    Given MCP project trust is "allow"
    And a workspace git repository "repo" with branches "main, dev"
    And repository "repo" branch "dev" declares the project MCP server "dev-probe"
    And a session rooted at folder "repo"
    When I switch the session to branch "dev"
    Then the context reports a git repository on branch "dev"
    And the session's configured MCP clients are ""

  Scenario: A workspace switch during a running turn is refused
    Given a workspace folder "alpha" without git
    And a workspace folder "beta" without git
    And a session rooted at folder "alpha"
    And a prompt turn is in flight for the session
    When I switch the session workspace to folder "beta"
    Then the workspace request fails with status 409
    When the turn completes
    Then the context path points to folder "alpha"

  Scenario: An invalid folder is refused before a session is created
    When a fresh session id switches its workspace to a folder that does not exist
    Then the workspace request fails with status 400
    And no session was created

  Scenario: A locked session keeps its 409 even for an invalid folder
    Given a workspace folder "alpha" without git
    And a session rooted at folder "alpha"
    And the session already has a user message
    When I switch the session workspace to a folder that does not exist
    Then the workspace request fails with status 409

  Scenario: A running turn keeps its 409 even for an invalid folder
    Given a workspace folder "alpha" without git
    And a session rooted at folder "alpha"
    And a prompt turn is in flight for the session
    When I switch the session workspace to a folder that does not exist
    Then the workspace request fails with status 409
    When the turn completes

  Scenario: Opening the branch list fetches every remote first
    Given a workspace git repository "repo" with branches "main, feature/login"
    And repository "repo" has a second remote "upstream"
    And branch "feature/fresh" was pushed to remote "origin" of repository "repo" after its last fetch
    And branch "fix/upstream-only" was pushed to remote "upstream" of repository "repo" after its last fetch
    And repository "repo" has uncommitted changes
    And a session rooted at folder "repo"
    When I request the workspace context
    Then the context lists no remote-only branch "feature/fresh"
    When I refresh the remote branches
    Then the refresh reports status "ok" for remotes "origin, upstream"
    And the context lists the remote-only branches "feature/fresh, fix/upstream-only"
    And the context reports a git repository on branch "main"
    And repository "repo" still has its uncommitted changes

  Scenario: A branch deleted on the remote leaves the list at the next refresh
    Given a workspace git repository "repo" with branches "main"
    And branch "feature/gone" was pushed to remote "origin" of repository "repo" after its last fetch
    And branch "feature/kept" was pushed to remote "origin" of repository "repo" after its last fetch
    And a session rooted at folder "repo"
    And the session refreshed the remote branches
    And the context lists the remote-only branches "feature/gone, feature/kept"
    When branch "feature/gone" is deleted from remote "origin" of repository "repo"
    And I refresh the remote branches
    Then the refresh reports status "ok" for remotes "origin"
    And the context lists the remote-only branches "feature/kept"

  Scenario: The branch list of a new chat is refreshed for the folder it picked
    Given a workspace git repository "repo" with branches "main"
    And branch "feature/fresh" was pushed to remote "origin" of repository "repo" after its last fetch
    When I refresh the remote branches of folder "repo" before a session exists
    Then the refresh reports status "ok" for remotes "origin"
    And the context lists the remote-only branches "feature/fresh"

  Scenario: Picking a remote-only branch checks it out tracking the remote
    Given a workspace git repository "repo" with branches "main"
    And repository "repo" has a second remote "upstream"
    And branch "fix/upstream-only" was pushed to remote "upstream" of repository "repo" after its last fetch
    And a session rooted at folder "repo"
    And the session refreshed the remote branches
    When I switch the session to branch "fix/upstream-only"
    Then the context reports a git repository on branch "fix/upstream-only"
    And the context reports the session is not in a worktree
    And branch "fix/upstream-only" of repository "repo" tracks "upstream/fix/upstream-only"

  Scenario: Opening a remote-only branch in a dedicated worktree
    Given a workspace git repository "repo" with branches "main"
    And repository "repo" has a second remote "upstream"
    And branch "fix/upstream-only" was pushed to remote "upstream" of repository "repo" after its last fetch
    And a session rooted at folder "repo"
    And the session refreshed the remote branches
    When I switch the session to branch "fix/upstream-only" in a worktree
    Then the context reports a git repository on branch "fix/upstream-only"
    And the context reports the session is in a worktree
    And the worktree path is ".coddy/worktrees/fix-upstream-only" inside repository "repo"
