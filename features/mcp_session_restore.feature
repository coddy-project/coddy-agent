Feature: A stored conversation starts its MCP servers only to run a turn
  A web page, a chat or an editor loads a stored session to read it as often
  as to continue it: its transcript, its activity, its stats. Loading one used
  to start every configured MCP server for it and keep the processes for the
  life of the server, so browsing a history of one-off sessions left a process
  per session behind. A session restored from disk now starts its configured
  servers before its first turn, and reading it starts nothing.

  Scenario: Reading a stored session starts no MCP server, its next turn does
    Given a stored session for a workspace
    And a workspace whose global mcp.json runs a marker command
    When a web page reads that stored session
    Then the marker command has not run
    When a turn runs on that stored session
    Then the marker command has run
