Feature: Sessions share their MCP servers
  Every session used to start a process of its own for every configured MCP
  server, and every subagent it spawned one more: a coddy serve with three
  conversations ran three copies of the same browser-automation server. The
  configured servers now come from one pool per process. A server of the
  Coddy home's mcp.json starts with coddy serve, the console or coddy acp and
  stays up for as long as the process runs, one copy for every session. A
  server of a project's .coddy/mcp.json runs once per workspace for the
  sessions of that workspace, and stops mcp.idle_timeout_seconds (five
  minutes by default) after the last of them closes, unless another session
  of the workspace takes it in the meantime. A linked git worktree is the same
  project as its main checkout: a project server the two declare alike runs
  once for both, approved once, while a server only the worktree declares
  waits for its own approval.

  Scenario: A global server runs once for the whole process
    Given the home mcp.json declares the MCP server "shared"
    When the process starts its global MCP servers
    Then the MCP server "shared" runs before any session opens
    When a session opens in one workspace
    And another session opens in another workspace
    Then both sessions call the MCP server "shared"
    And the MCP server "shared" was started once
    When both sessions close
    Then the MCP server "shared" is still running

  Scenario: A project server runs once per workspace and stops after its last session
    Given two workspaces whose project mcp.json declares the approved MCP server "proj"
    And an MCP server no session holds stops after 1 second
    When two sessions open in the first workspace
    Then the MCP server "proj" was started once
    When a session opens in the second workspace
    Then the MCP server "proj" was started twice
    When both sessions of the first workspace close
    Then the MCP server "proj" of the first workspace has stopped
    And the MCP server "proj" of the second workspace is still running

  Scenario: A worktree shares the project servers of its main checkout and asks only about its own
    Given a project whose main checkout declares the approved MCP server "proj"
    And a worktree of the project that declares "proj" alike and a new MCP server "lab"
    When a session opens in the main checkout
    And a session opens in the worktree
    Then the MCP server "proj" was started once
    And both sessions call the MCP server "proj"
    And the MCP server "lab" waits for approval in the worktree
