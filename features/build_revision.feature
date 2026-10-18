Feature: A binary built in a git worktree names the commit of that worktree
  The Go toolchain stamps a binary with the commit of the checkout it was built
  in, and it recognises a checkout only by a `.git` directory. A git worktree
  has a `.git` file instead, so a worktree nested in a folder of another
  checkout - where Coddy, Claude Code and other agent tools put theirs - was
  stamped with the enclosing checkout's commit, and the `coddy_build_info`
  series and Settings -> System showed that commit next to the worktree's own
  version (issue #547). The build now names the commit itself, the way it
  names the version.

  Scenario: make build in a worktree nested inside another checkout names the worktree's commit
    Given a checkout with a worktree nested in a folder of it, each on a commit of its own
    And a program built in the worktree, which the Go toolchain stamps with the enclosing checkout's commit
    When the Makefile of the repository names the revision for a build in the worktree
    Then it names the worktree's commit
    And the program, given that revision, describes itself by the worktree's commit
