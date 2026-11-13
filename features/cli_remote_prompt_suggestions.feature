Feature: Follow-up suggestions from a remote server
  The remote server generates predictions using its own model and history.
  The console displays the hint and sends acceptance back to that server.

  Scenario Outline: Accept a remote suggestion
    Given a remote console whose server suggests "<text>"
    When the remote console turn finishes
    Then the remote editor shows the dim hint "<text>"
    When the remote hint is accepted with <key> and submitted
    Then the server receives an accepted suggestion outcome
    And only one remote suggestion was requested

    Examples:
      | text          | key   |
      | run the tests | Tab   |
      | run the tests | Right |
      | продолжай     | Tab   |
      | продолжай     | Right |

  Scenario: The server suppresses the suggestion
    Given a remote console whose server suggests ""
    When the remote console turn finishes
    Then the remote editor shows no hint
    And only one remote suggestion was requested
