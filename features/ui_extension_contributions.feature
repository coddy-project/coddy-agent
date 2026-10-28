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

