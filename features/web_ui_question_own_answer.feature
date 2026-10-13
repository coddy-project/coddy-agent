Feature: The web UI offers an answer of the reader's own unless a question turns it off
  The options a model puts up often miss what the reader means (issue #257).
  A question's custom flag is on by default: the question card lets the reader
  type an answer of their own, and the row that records the question keeps
  that slot. A question that turns the flag off lists its options alone.

  Scenario: The question card takes a typed answer on single and multiple choice
    Then every question of the card offers an answer of the reader's own

  Scenario: A question that turned custom off offers only its options
    Then a question of the card that turned custom off lists only its options

  Scenario: The record of a question keeps the free slot the card offered
    Then the record of a question keeps the free slot unless the question turned it off
