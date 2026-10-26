Feature: The todo checklist in the transcript read
  A client that opens a conversation learns its todo checklist from the read
  that brings the messages, so it shows the list without a second request and
  every later read of the transcript brings the list as it is then.

  Scenario: A transcript read carries the session's checklist
    Given a session whose checklist has an item done, one in progress and one pending
    When the client reads the transcript
    Then the read carries the checklist in its order with each item's status
