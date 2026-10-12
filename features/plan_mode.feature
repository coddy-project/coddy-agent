Feature: Plan mode keeps to its tool set when a call runs
  Plan mode offers the model a fixed set of tools: the read tools, the shell,
  the plan document tools and the tools of the connected MCP servers. A call
  that names any other tool anyway - one echoed from history recorded in agent
  mode, or one made by a provider that ignores the tool list it was given - is
  refused when it is about to run, so the boundary of plan mode holds even when
  the model does not respect the list. Running a saved plan is how the work
  leaves plan mode: that turn runs in agent mode with the full tool set, even
  when plan mode was armed for the next turns with --count.

  Scenario: A tool outside the plan tool set is refused instead of executed
    Given a coddy session in "plan" mode
    And a model that requests the "write" tool once, then answers
    When the user asks for a plan
    Then the file is not written
    And the tool call is answered with the plan-mode refusal
    And the turn ends with the model's answer

  Scenario: A plan document is still written in plan mode
    Given a coddy session in "plan" mode
    And a model that requests the "plan_write" tool once, then answers
    When the user asks for a plan
    Then the plan document is written
    And the file is not written
    And the tool call is answered without a refusal
    And the turn ends with the model's answer

  Scenario: Running the plan implements it in agent mode while plan mode is armed for the next turns
    Given a coddy session with plan mode armed for the next 3 turns
    And a saved design plan
    And a model that requests the "write" tool once, then answers
    When the user runs the plan
    Then the file is written
    And the tool call is answered without a refusal
    And the session is in agent mode with no mode armed for the next turns
    And the turn ends with the model's answer
