Feature: Background work resumes its parent by default
  The parent may finish its turn after starting background work. Unless it
  explicitly disables notification, completion starts a new parent turn.

  Scenario: A background command wakes its parent without a notify flag
    Given a parent session with a background waker
    When the parent starts a background command without notify_on_finish and ends its turn
    Then completion starts a new parent turn for the command

  Scenario: A background subagent wakes its parent without a notify flag
    Given a parent session with a background waker
    And an approved subagent named "reviewer"
    When the parent starts that subagent in the background without notify_on_finish and ends its turn
    Then completion starts a new parent turn for the subagent
