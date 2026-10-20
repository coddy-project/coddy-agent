Feature: Model memory on the console
  The console is a surface of its own: a new session starts on the model the
  operator last picked on this console, and the console's very first use
  starts on the alphabetically first configured model. The configured default
  (agent.model) remains only where a surface never chose. A model named on
  the command line with --model is the operator's choice for that run and
  beats the remembered one.

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

  Scenario: The remembered model is on the session when startup returns
    Given a coddy console app over a stub agent runner with models "stub/aaa-model", "stub/model-two" and "stub/zed-model" and default model "stub/zed-model"
    And the console remembers the model "stub/model-two"
    When the console app starts
    Then the session is already on the model "stub/model-two"

  Scenario: An explicit --model beats the remembered model
    Given a coddy console app over a stub agent runner with models "stub/aaa-model", "stub/model-two" and "stub/zed-model" and default model "stub/zed-model"
    And the console remembers the model "stub/model-two"
    When the console app starts with the model "stub/aaa-model" on its command line
    Then the session settles on the model "stub/aaa-model"
    And the footer names the model "(stub) aaa-model"

  Scenario: An explicit --model beats the remembered model with --resume
    Given a coddy console app over a stub agent runner with models "stub/aaa-model", "stub/model-two" and "stub/zed-model" and default model "stub/zed-model"
    And the console remembers the model "stub/model-two"
    When the console app starts with --resume and the model "stub/aaa-model" on its command line
    Then the session settles on the model "stub/aaa-model"
    And the footer names the model "(stub) aaa-model"
