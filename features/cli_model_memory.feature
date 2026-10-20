Feature: Model memory on the console
  The console is a surface of its own: a new session starts on the model the
  operator last picked on this console, and the console's very first use
  starts on the alphabetically first configured model. The configured default
  (agent.model) remains only where a surface never chose. A model named on
  the command line with --model is the operator's choice for that run and
  beats the remembered one, however late the remembered model's write reaches
  the session.

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
    And the write of the model "stub/model-two" into the session lands late
    When the console app starts
    Then the session is already on the model "stub/model-two"

  Scenario: An explicit --model beats the remembered model
    Given a coddy console app over a stub agent runner with models "stub/aaa-model", "stub/model-two" and "stub/zed-model" and default model "stub/zed-model"
    And the console remembers the model "stub/model-two"
    And the write of the model "stub/model-two" into the session lands late
    When the console app starts with the model "stub/aaa-model" on its command line
    Then the session settles on the model "stub/aaa-model"
    And the footer names the model "(stub) aaa-model"

  Scenario: An explicit --model beats the remembered model with --resume
    Given a coddy console app over a stub agent runner with models "stub/aaa-model", "stub/model-two" and "stub/zed-model" and default model "stub/zed-model"
    And the console remembers the model "stub/model-two"
    And the write of the model "stub/model-two" into the session lands late
    When the console app starts with --resume and the model "stub/aaa-model" on its command line
    Then the session settles on the model "stub/aaa-model"
    And the footer names the model "(stub) aaa-model"

  Scenario: A refused write of the initial model is reported at launch
    Given a coddy console app over a stub agent runner with models "stub/aaa-model", "stub/model-two" and "stub/zed-model" and default model "stub/zed-model"
    And the console remembers the model "stub/model-two"
    And the backend can refuse the write of the model "stub/model-two" into a session
    And the backend refuses that write from now on
    When the console app starts
    Then the transcript shows "model: provider refused stub/model-two"

  Scenario: A refused write of the initial model is reported when a new session starts
    Given a coddy console app over a stub agent runner with models "stub/aaa-model", "stub/model-two" and "stub/zed-model" and default model "stub/zed-model"
    And the console remembers the model "stub/model-two"
    And the backend can refuse the write of the model "stub/model-two" into a session
    When the console app starts
    And the backend refuses that write from now on
    And the operator starts a new session
    Then the transcript shows "model: provider refused stub/model-two"
