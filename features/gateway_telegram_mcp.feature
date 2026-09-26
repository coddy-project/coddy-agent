Feature: Switching MCP servers from a Telegram chat
  /mcp answers with the MCP servers the workspace sees - the operator's own
  from config.yaml and <home>/mcp.json, the project's from .coddy/mcp.json -
  each with its status and the number of tools it offers. A server that may
  run in this workspace gets a button that switches it off or on; the switch
  is saved where the server is declared, and the live sessions are told which
  server changed. A project server nobody approved gets no button: approving
  it means reading the command it would run, which the console, the web UI
  and the CLI show in full, so trust is never granted from a chat.

  Background:
    Given a telegram gateway over a workspace
    And a global MCP server "docs" offering the tools "echo" and "reverse"
    And a project MCP server "checkout" that nobody approved

  Scenario: The menu lists every server and offers a switch for the trusted one
    When the user sends "/mcp"
    Then the menu lists "docs · connected · 2 tools"
    And the menu lists "checkout · needs_approval · 0 tools"
    And the menu offers the button "Disable docs"
    And the menu offers no button for "checkout"

  Scenario: A tap switches a global server off
    Given the user has been sent the MCP menu
    When the user taps "Disable docs"
    Then the global mcp.json records "docs" as disabled
    And the live sessions are told to refresh "docs"
    And the menu lists "docs · disabled · 0 tools"
    And the menu offers the button "Enable docs"

  Scenario: A tap switches it back on
    Given the global MCP server "docs" is switched off
    And the user has been sent the MCP menu
    When the user taps "Enable docs"
    Then the global mcp.json records "docs" as enabled
    And the live sessions are told to refresh "docs"
    And the menu lists "docs · connected · 2 tools"
    And the menu offers the button "Disable docs"
