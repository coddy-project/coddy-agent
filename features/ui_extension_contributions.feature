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

  Scenario: Watch the browser window lifecycle
    Given an extension watching focus and unloading
    When the window focus changes and unloading starts
    Then the extension receives focus and beforeunload events

  Scenario: Read parsed chat activity and generation progress
    Given a running chat with an answer and a generation rate
    When the extension reads the chat status
    Then it receives the activity and parsed progress fields

  Scenario: Mount beside the composer model and send controls
    Given a composer with model and send controls
    When the extension asks for the fine composer anchors
    Then the card, model and send anchor boxes are available

  Scenario: Read the saved provider catalog without credentials
    Given a configured provider with credentials and advertised models
    When the extension lists providers and their models
    Then provider rows omit credentials and model rows are normalized

  Scenario: Keep the extension theme-token contract stable
    Given an extension that injects a private token
    When it reads the versioned token contract
    Then the host token shape stays fixed and unknown overrides are refused

  Scenario: Stream a browser response and use a lifetime-bound WebSocket
    Given a streaming browser response and a WebSocket endpoint
    When the extension reads chunks and its lifetime ends
    Then chunks arrive incrementally and the socket closes
