Feature: Follow-up suggestions in the local console
  After the agent's first reply, the operator can accept a dim hint in the
  empty editor without adding the prediction to the conversation.

  Scenario Outline: Accept a suggestion after the first completed reply
    Given a local console whose model predicts "<suggestion>"
    When the first console turn finishes
    Then the empty editor shows "<suggestion>" in its dim color
    And the prediction leaves the conversation unchanged
    When the operator accepts the console hint with <key>
    Then the console draft is "<suggestion>"
    And only one console prediction was requested

    Examples:
      | suggestion    | key   |
      | run the tests | Tab   |
      | run the tests | Right |
      | продолжай     | Tab   |
      | продолжай     | Right |

  Scenario: Disable console suggestions in the configuration
    Given a local console whose model predicts "run the tests"
    And console prompt suggestions are disabled
    When the first console turn finishes
    Then the console editor offers no hint
    And no console prediction was requested
    And the prediction leaves the conversation unchanged
