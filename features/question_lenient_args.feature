Feature: Question tool accepts model-encoded argument forms
  Models sometimes double-encode tool arguments: the questions array arrives
  as a JSON string instead of a JSON array. The question tool asks the user
  either way instead of failing the call.

  Scenario: A canonical questions array reaches the user
    Given a question sender that answers "A"
    When the question tool runs with arguments:
      """
      {"questions":[{"question":"Pick one","options":[{"label":"A"},{"label":"B"}]}]}
      """
    Then the user was asked "Pick one"
    And the tool returns the answer "A"

  Scenario: A questions array wrapped in a JSON string still reaches the user
    Given a question sender that answers "A"
    When the question tool runs with arguments:
      """
      {"questions":"[{\"question\":\"Pick one\",\"options\":[{\"label\":\"A\"},{\"label\":\"B\"}]}]"}
      """
    Then the user was asked "Pick one"
    And the tool returns the answer "A"
