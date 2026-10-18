Feature: Move a chat between the main checkout and a worktree with /worktree
  The built-in /worktree command does for the operator what the agent's
  worktree_open and worktree_exit tools do for the model, from the console,
  the web UI or an editor: open a worktree for a branch and move the chat
  there, or bring the chat back to the main checkout. It runs without a model
  turn, and the web UI's plate sends "/worktree exit" to bring a chat back.

  Scenario: The operator opens a worktree for a branch and brings the chat back
    Given a coddy server with a chat in a repository with a bare origin
    When the operator sends "/worktree feature/ui"
    Then the chat works in the worktree for "feature/ui"
    And the reply names the worktree for "feature/ui"
    When the operator sends "/worktree exit"
    Then the chat works in the main checkout
    And the "/worktree exit" command is part of the transcript
