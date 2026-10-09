Feature: A session goal is worked on until a second model confirms it
  /goal sets an objective and starts working on it at once. After every turn
  the supervisor checks the result against the objective, using the tool
  results as evidence, and starts a continuation that names what is left until
  the check is met.

  Scenario: A goal set with /goal continues until the check is met
    Given an agent session whose model ships the fix only on its second turn
    When the operator sends "/goal ship the fix"
    Then the first turn is the goal kickoff
    And the supervisor continues the work naming what is left
    And the goal is complete after one continuation
    And no supervisor turn reads as a message the operator typed

  Scenario: A check that needs the operator stops the goal
    Given an agent session whose model asks which database to use
    When the operator sends "/goal migrate the store"
    Then the goal is blocked with the question for the operator
    And no continuation was started
