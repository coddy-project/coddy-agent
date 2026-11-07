Feature: A new chat is named with the selected model
  Scenario: The selected model names a chat before its session exists
    Given a running coddy HTTP server
    And the default title model is unavailable
    And the selected title model "openai/gpt-4o-mini" answers "Repair session naming" with tags "sessions, titles"
    When a new chat asks for a description of "repair session naming" with model "openai/gpt-4o-mini"
    Then the selected title model was used instead of the default
    And the description is "Repair session naming"
    And the description proposes tags "sessions, titles"
