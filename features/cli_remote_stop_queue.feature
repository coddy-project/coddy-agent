Feature: Stop and queue a shared remote turn from the console
  A console joining a browser-owned turn can queue follow-ups and stop it
  without opening a competing prompt or taking ownership of that request.

  Scenario: Join a browser turn, queue a correction, stop, and send again
    Given a browser is running a turn with a queued message in the shared remote session
    When the console reopens that session and hydrates its controls
    And the console operator submits the follow-up "also check Windows"
    Then the follow-up is queued without a new prompt request
    When the console operator presses Escape
    Then the remote turn is cancelled and its queue is empty
    When the console operator submits "start the next task"
    Then the console starts a new prompt request
