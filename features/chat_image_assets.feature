Feature: Generated images belong to the assistant chat message
  Scenario: A native image answer survives a chat reload
    Given a persisted chat with a native image Responses provider
    When the chat requests an image with explicit generation enabled
    Then the chat streams an assistant image asset
    And the reloaded assistant message owns the generated image
    And the session asset route returns the original image
