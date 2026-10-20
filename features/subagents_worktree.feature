Feature: A subagent works in a git worktree of its own
  A child that edits files in the parent's checkout mixes its changes with the
  parent's. With isolation worktree, asked for by its definition or by the
  spawn_agent call, the child works in a worktree of its own, on a new branch
  at the commit the parent's checkout is on. A run that leaves nothing there
  removes the worktree and its branch when it ends; a run that leaves work keeps
  them, and the parent is told where.

  Scenario: A child that changes nothing leaves no worktree behind
    Given a git repository as the workspace with a subagent definition "fixer" that works in a worktree
    And a parent agent session in that workspace
    And the workspace definition "fixer" is approved for that workspace
    When the parent model spawns "fixer" in the foreground and the child answers "REPORT: nothing to change"
    Then the child session worked in a worktree of the repository, not in the parent's folder
    And the spawn_agent tool result contains "REPORT: nothing to change"
    And the repository has no worktree and no branch of the child left

  Scenario: A child's edits stay in its worktree and the parent learns where
    Given a git repository as the workspace with a subagent definition "fixer" that works in a worktree
    And a parent agent session in that workspace
    And the workspace definition "fixer" is approved for that workspace
    When the parent model spawns "fixer" in the foreground and the child writes "fix.txt" before it answers "REPORT: fixed"
    Then the parent's folder has no "fix.txt"
    And the child's worktree keeps "fix.txt"
    And the spawn_agent tool result names the child's worktree and its branch

  Scenario: The call asks for a worktree for a definition that does not
    Given a git repository as the workspace with a subagent definition "reviewer" under .coddy/agents
    And a parent agent session in that workspace
    And the workspace definition "reviewer" is approved for that workspace
    When the parent model spawns "reviewer" in a worktree of its own and the child answers "REPORT: looked"
    Then the child session worked in a worktree of the repository, not in the parent's folder
    And the repository has no worktree and no branch of the child left
