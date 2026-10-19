Feature: Leave a feature worktree, and stack a branch on the current work
  An agent that opened a worktree for a feature branch goes back to the main
  checkout in one call once the work there is done. A worktree that holds
  nothing of its own goes on the way, one with work in it stays for later, and
  the agent can keep or remove it explicitly. It can start the next branch from
  the commit it stands on instead of from the default branch.

  Scenario: The agent goes back to the main checkout and keeps the worktree
    Given a session in a repository with a bare origin
    And the agent works in a worktree for "feature/login"
    When the agent leaves the worktree and keeps it
    Then the session works in the main checkout again
    And the worktree for "feature/login" is still there

  Scenario: Leaving a worktree that holds nothing of its own removes it
    Given a session in a repository with a bare origin
    And the agent works in a worktree for "feature/look"
    When the agent leaves the worktree
    Then the session works in the main checkout again
    And the worktree for "feature/look" is gone
    And the branch "feature/look" is gone

  Scenario: Leaving a worktree with work of its own keeps it and says why
    Given a session in a repository with a bare origin
    And the agent works in a worktree for "feature/work" with a commit of its own
    When the agent leaves the worktree
    Then the session works in the main checkout again
    And the worktree for "feature/work" is still there
    And the answer says the worktree stays for its commit

  Scenario: The agent removes a finished worktree on its way back
    Given a session in a repository with a bare origin
    And the agent works in a worktree for "feature/done"
    When the agent leaves the worktree and removes it
    Then the session works in the main checkout again
    And the worktree for "feature/done" is gone
    And the branch "feature/done" is gone

  Scenario: A branch started from the current commit carries the work of the worktree it was opened from
    Given a session in a repository with a bare origin
    And the agent works in a worktree for "feature/base" with a commit of its own
    When the agent creates a worktree for "feature/next" from the current commit
    Then the session works in the worktree for "feature/next"
    And the branch "feature/next" starts at the commit of "feature/base"

  Scenario: The agent sees where every worktree stands and moves into one of them
    Given a session in a repository with a bare origin
    And the agent works in a worktree for "feature/a" with a commit of its own
    And the agent works in a worktree for "feature/b" and leaves a file uncommitted there
    When the agent lists the worktrees
    Then the list shows "feature/a" one commit ahead of the default branch
    And the list shows "feature/b" with one uncommitted change, as the worktree the session works in
    When the agent enters the worktree of "feature/a" by its folder
    Then the session works in the worktree for "feature/a"
