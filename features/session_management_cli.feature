Feature: Manage persisted sessions from the command line
  Sessions can be listed and inspected from disk. Changes go through the
  running manager rather than editing bundles from the CLI.

  Scenario: Inspect and update a saved session
    Given the CLI has a saved session "sess_review"
    When I run sessions "list --format json"
    Then the JSON list contains "sess_review"
    When I run sessions "show sess_rev --format json"
    Then the JSON object has "id" equal to "sess_review"
    When I run sessions "update sess_rev --title Reviewed --tags work,done --archived=true --format json"
    Then the manager receives "PATCH" for "sess_review"
    And the JSON object has "title" equal to "Reviewed"

  Scenario: Confirm deletion of a saved session in a terminal
    Given the CLI has a saved session "sess_review"
    And I answer yes at the terminal
    When I run sessions "delete sess_rev"
    Then the manager receives "DELETE" for "sess_review"
    And the output reports deletion of "sess_review"
