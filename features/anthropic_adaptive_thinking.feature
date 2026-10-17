Feature: Anthropic adaptive thinking preserves effort and tool continuity
  Claude Opus 4.7 and later models refuse a thinking budget: Coddy asks them
  for adaptive thinking, with the selected level as the output effort and the
  summarized display, so the chat shows their reasoning. Older models keep
  their extended-thinking token budgets. Every signed thinking block of an
  answer goes back unchanged, in its place, when the tool loop continues.

  Scenario Outline: Continue a signed thinking tool call
    Given Anthropic model "<model>" with reasoning effort "<effort>"
    When Coddy streams a tool call and continues with its result
    Then both requests use "<thinking>" thinking and output effort "<output>"
    And signed thinking is replayed unchanged before tool use

    Examples:
      | model             | effort | thinking | output |
      | claude-opus-5-5   | high   | adaptive | high   |
      | claude-opus-5-5   |        | adaptive |        |
      | claude-sonnet-5   | low    | adaptive | low    |
      | claude-sonnet-4-5 | high   | enabled  |        |

  Scenario: Continue an answer with several signed thinking blocks
    Given Anthropic model "claude-opus-5-5" answering with a note, a progress update and a tool call
    When Coddy streams a tool call and continues with its result
    Then every thinking block is replayed with its own signature in the order the model wrote it
