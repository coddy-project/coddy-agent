Feature: A binary built in a git worktree names the commit of that worktree
  The Go toolchain stamps a binary with the commit of the checkout it was built
  in, and up to Go 1.26 it recognises a checkout only by a `.git` directory. A
  git worktree has a `.git` file instead, so a worktree nested in a folder of
  another checkout - where Coddy and Claude Code put theirs - was stamped with
  the enclosing checkout's commit, and the `coddy_build_info` series and
  Settings -> System showed that commit next to the worktree's own version
  (issue #547). The build now names the commit itself, the way it names the
  version, so the binary names the worktree's commit whichever of the two the
  toolchain stamps: Go 1.27 stamps the worktree's own (issue #610).

  Scenario: make build in a worktree nested inside another checkout names the worktree's commit
    Given a checkout with a worktree nested in a folder of it, each on a commit of its own
    When the Makefile of the repository builds the binary in the worktree
    Then the Go toolchain has stamped the binary with the commit of one of the two checkouts
    And the build has named the worktree's commit as the binary's revision
    And the binary describes itself by the worktree's commit
