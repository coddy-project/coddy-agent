Feature: A question offers an answer of the user's own unless it turns that off
  The options a model puts up often miss what the user means (issue #257).
  The custom flag of a question is on by default: whatever the model leaves
  out, the user can type an answer of their own, and the typed text reaches
  the model as the answer. A model that needs one of its options exactly
  turns the flag off for that question.

  Scenario: A question the model asked without the custom flag still takes a typed answer
    Given a question sender that answers "use the staging database"
    When the question tool runs with arguments:
      """
      {"questions":[{"question":"Which database?","options":[{"label":"postgres"},{"label":"sqlite"}]}]}
      """
    Then the user was asked "Which database?"
    And every question the user was asked offers an answer of their own
    And the tool returns the answer "use the staging database"

  Scenario: A question the model turns custom off offers only its options
    Given a question sender that answers "yes"
    When the question tool runs with arguments:
      """
      {"questions":[{"question":"Delete the branch?","options":[{"label":"yes"},{"label":"no"}],"custom":false}]}
      """
    Then the user was asked "Delete the branch?"
    And the question "Delete the branch?" offers only its options
    And the tool returns the answer "yes"

  Scenario: The tool tells the model the free answer is on by default
    Then the question tool's custom flag is on by default
    And the question tool's description says the user can type an answer of their own
