Feature: Model memory on the console
  The console is a surface of its own: a new session starts on the model the
  operator last picked on this console, and the console's very first use
  starts on the alphabetically first configured model. The configured default
  (agent.model) remains only where a surface never chose.

  Scenario: First launch starts on the alphabetically first model
    Given a coddy console app over a stub agent runner with models "stub/zed-model" and "stub/aaa-model" and default model "stub/zed-model"
    When the console app starts
    Then the session state records the model "stub/aaa-model"

  Scenario: A picked model is what the next new session starts on
    Given a coddy console app over a stub agent runner
    When the console app starts
    And the operator switches the model to "stub/model-two"
    And the operator starts a new session
    Then the session state records the model "stub/model-two"
