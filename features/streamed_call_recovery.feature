Feature: Recover a malformed tool call without executing the discarded batch
  Scenario: A replacement reuses a discarded id in a session without a bundle
    Given the model announces a read in a batch rejected as malformed
    When the model sends a replacement read with the same id
    Then only the replacement has a paired result and its own completed row
