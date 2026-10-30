Feature: Preserve a follow-up suggestion while editing
  The finished turn's suggestion stays as the composer's placeholder until
  the operator sends a message or leaves that turn's context.

  Scenario Outline: Return to the same suggestion after clearing a draft
    Then editing and clearing the composer preserves the suggestion for "<key>"

    Examples:
      | key        |
      | Tab        |
      | ArrowRight |
