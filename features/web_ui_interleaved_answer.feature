Feature: An answer whose text and reasoning interleave is shown once
  An ACP agent served as a model thinks between two parts of one answer: its
  text, then its reasoning and its tool calls, then more text, all in one turn.
  The live view draws each part where it came. At the end of the turn the
  persisted message replaces the parts and keeps the id of one of them, and the
  turn's answer is shown once: the end of the turn looks for it under every
  part, and does not bring the last part back holding the whole answer.

  Scenario: The second part of an interleaved answer does not come back after the turn
    Then a turn whose text and reasoning interleave records every segment of its answer
    And a turn whose merged answer carries the id of its first segment counts as answered
