Feature: Export a stored session from the command line
  `coddy sessions export` is the shell twin of the built-in /export command,
  the way `coddy plugin` twins /plugin. It reads a persisted session bundle
  from the sessions root and writes the same export document into the
  current directory, or wherever --out points, without starting an agent.

  Background:
    Given a sessions root holding the session "sess_export_demo" with 2 completed exchanges
    And the shell runs in an empty output directory

  Scenario: A session id exports markdown into the current directory
    When I run coddy sessions export "sess_export_demo"
    Then the command prints "Session exported to markdown:"
    And a "coddy-export-*.md" file exists in the output directory
    And the exported file contains "question 1"
    And the exported file contains "canned answer 2"

  Scenario: A unique id prefix, a format flag and a relative output path
    When I run coddy sessions export "sess_exp --format json --out exports/chat.json"
    Then the command prints "Session exported to JSON:"
    And the file "exports/chat.json" exists in the output directory
    And the exported JSON document is the session "sess_export_demo" with 4 entries

  Scenario: The format follows the extension of an absolute output path
    When I run coddy sessions export "sess_export_demo --out <outside>/reports/chat.html"
    Then the command prints "Session exported to HTML:"
    And the file "reports/chat.html" exists outside the output directory
    And the exported file contains "question 2"

  Scenario: Trimming options leave out tool calls and reasoning
    Given the stored session also holds a "read" tool call returning "README BODY" after reasoning "private thoughts"
    When I run coddy sessions export "sess_export_demo --out chat.md --no-tools --no-thinking"
    Then the file "chat.md" exists in the output directory
    And the exported file contains "canned answer 1"
    And the exported file does not contain "README BODY"
    And the exported file does not contain "private thoughts"
