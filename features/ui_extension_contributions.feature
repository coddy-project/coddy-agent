Feature: UI extension contributions
  Scenario: Listen to navigation and completed work
    Given extension listeners for navigation steps and turns
    When the user changes tabs and a model step and turn finish
    Then the extension receives the completed events with their context

  Scenario: Render an MCP call with extension code
    Given an extension renderer for a named MCP tool
    When the tool input and result change
    Then the custom card updates and original details remain available

  Scenario: Add a field to an existing settings section
    Given an extension field in the Agent settings section
    When the user edits its value
    Then the field updates the extension namespace in the configuration draft

  Scenario: Draw a stream frame the host has no row for
    Given an extension message renderer matching one frame event
    When a live turn stream carries a frame of that event
    Then the renderer draws one ext_frame row in the transcript

  Scenario: Decorate the permission card of a waiting tool call
    Given an extension permission preview for a named tool
    When a call of that tool waits for permission
    Then the preview appears under the host call preview and the host options remain

  Scenario: Open a panel from a menu item
    Given an extension panel and a menu action that opens it
    When the user selects the menu action
    Then the panel opens with the extension content

  Scenario: Repaint the app with a theme override
    Given an extension that overrides theme colours
    When the override replaces the accent colour
    Then the app repaints live and removing the override restores the theme

  Scenario: Hide session rows with an extension filter
    Given an extension session filter that hides archived rows
    When the History listing holds archived and active sessions
    Then the listing shows only the rows every extension filter passes

  Scenario: The History listing opens on the current project
    Given sessions of two projects
    When the History listing opens in one of them
    Then it lists the sessions of that project
