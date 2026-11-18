Feature: Explain provider tool schema limits
  Scenario: A simple message is rejected before any tool runs
    Given a provider that rejects the offered tool parameter schemas as too large
    When the user sends a simple greeting with tools available
    Then the turn explains how to reduce the offered tool schemas
    And the rejected request is sent only once and no tool runs
