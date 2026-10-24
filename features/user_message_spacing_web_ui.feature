Feature: User prompt spacing and controls
  Follow-up prompts keep the same room above their bubble as their footer adds below it.
  The edit control returns a prompt to the composer without changing history until resend.

  Scenario: A follow-up prompt keeps balanced room and readable actions
    Then follow-up spacing follows the global user index rather than the visible row position
    And follow-up prompts balance the footer room with shared spacing variables
    And the user foot keeps Copy before circular Edit and time, with Undo first when offered
