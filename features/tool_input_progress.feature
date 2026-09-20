Feature: File tool input progress
  The operator sees a draft while the model prepares a file, before any file is written.

  Scenario: A streamed write shows progress and then executes the full arguments
    When a file tool streams its arguments before executing
    Then the completed file contains the full draft
    And the final token count uses provider usage without double counting
