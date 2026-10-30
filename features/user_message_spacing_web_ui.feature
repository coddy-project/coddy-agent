Feature: User prompt spacing and controls
  A follow-up prompt keeps one gap above its bubble: the footer of the prompt before it
  already stands between the two, so short exchanges do not spread over the screen.
  The edit control returns a prompt to the composer without changing history until resend.

  Scenario: A follow-up prompt stays close and keeps readable actions
    Then follow-up spacing follows the global user index rather than the visible row position
    And a follow-up prompt keeps one gap above it and its footer room below
    And the user foot keeps Copy before circular Edit and time, with Undo first when offered
