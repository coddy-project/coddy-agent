Feature: Export the session history to a file with the /export command
  The built-in /export command writes the current conversation to a file in
  the session workspace, the way qwen-code's /export does. It runs
  deterministically without an LLM turn over every prompt surface (here the
  HTTP /v1/responses endpoint) and renders markdown, HTML, JSON, or JSON Lines.
  The exported document holds the conversation as it was before the command,
  while the command itself still lands in the transcript like every other
  built-in.

  Background:
    Given a running coddy export server
    And an export session with 2 completed exchanges

  Scenario: Bare /export writes a markdown transcript into the workspace
    When the user sends "/export" as an export prompt
    Then the export response confirms a "markdown" export
    And a "coddy-export-*.md" file exists in the workspace
    And the exported file contains the user prompt "question 1"
    And the exported file contains the assistant reply "canned answer"
    And the exported file does not contain the "/export" command
    And the "/export" command is part of the transcript

  Scenario Outline: Each format lands in its own default file
    When the user sends "/export <format>" as an export prompt
    Then the export response confirms a "<label>" export
    And a "coddy-export-*.<ext>" file exists in the workspace
    And the exported file contains the user prompt "question 2"

    Examples:
      | format | label      | ext   |
      | md     | markdown   | md    |
      | html   | HTML       | html  |
      | json   | JSON       | json  |
      | jsonl  | JSON Lines | jsonl |

  Scenario: An explicit path inside the workspace names the file
    When the user sends "/export md notes/chat.md" as an export prompt
    Then the export response confirms a "markdown" export
    And the file "notes/chat.md" exists in the workspace
    And the exported file contains the assistant reply "canned answer"

  Scenario: The format follows the extension of an explicit path
    When the user sends "/export chat.json" as an export prompt
    Then the export response confirms a "JSON" export
    And the file "chat.json" exists in the workspace
    And the exported JSON document lists 4 transcript entries

  Scenario: The full export keeps tool calls and reasoning
    Given the transcript also holds a "read" tool call returning "README BODY" after reasoning "private thoughts"
    When the user sends "/export md full.md" as an export prompt
    Then the file "full.md" exists in the workspace
    And the exported file contains the tool result "README BODY"
    And the exported file contains the reasoning "private thoughts"

  Scenario: A clean chat export leaves out tool calls and reasoning
    Given the transcript also holds a "read" tool call returning "README BODY" after reasoning "private thoughts"
    When the user sends "/export md chat.md --no-tools --no-thinking" as an export prompt
    Then the file "chat.md" exists in the workspace
    And the exported file contains the assistant reply "canned answer"
    And the exported file does not contain "README BODY"
    And the exported file does not contain "private thoughts"
