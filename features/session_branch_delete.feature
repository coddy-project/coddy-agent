Feature: Deleting a branch session keeps its parent openable
  Editing a user message forks the conversation, and both threads are recorded in
  the branch file of the source bundle. Deleting one of those sessions retracts it
  from that record, so the branch navigator never sends the UI into a bundle that
  is gone.

  Background:
    Given a running coddy HTTP server
    And a stored session with 2 user messages

  Scenario: The parent forgets its only branch
    Given the session is branched at user message 1
    When I delete the branch session
    Then the parent session reports no branch points
    And the parent session still serves its messages

  Scenario: The parent keeps the branches that survive
    Given the session is branched at user message 1
    And the session is branched at user message 1
    When I delete the first branch session
    Then the parent session reports 2 threads at user message 1
    And no branch point references the deleted session
    And the surviving branch reports position 2 of 2
