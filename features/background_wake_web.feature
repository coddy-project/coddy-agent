Feature: A turn a finished background task started is marked as a wake for the web UI
  A task the model started with notify_on_finish starts a turn of its own when it
  ends. Nobody typed that turn's first message, so the server marks it as the
  wake - on the stream a browser watching the session reads, and in the
  transcript it loads after a reload - and the web UI never shows it as a
  message from the user. The task says on its row that it woke the agent. The
  browser is also where a permission prompt raised in such a turn is answered:
  the turn waits for it instead of being refused.

  Scenario: The woken turn is marked as the wake, live and after a reload
    Given a coddy serve HTTP API whose agent is a scripted model
    And a browser following the server's events
    When the user asks the agent to "start the tests" in the background
    Then the events stream announces a background wake of that session naming the task as "failed"
    And the woken turn's stream opens with a background wake naming the task as "failed" with exit code 2
    And the woken turn's stream carries the answer "The tests failed with exit 2."
    And the session messages keep the woken turn's first message as a background wake naming the task
    And the session's background tasks say the task woke the agent

  Scenario: A permission prompt raised in a woken turn waits for the web UI
    Given a coddy serve HTTP API whose agent is a scripted model that fixes what woke it
    And a browser following the server's events
    When the user asks the agent to "start the tests" in the background
    Then the woken turn's stream asks permission to run "touch fixed.txt"
    When the web UI allows it
    Then the woken turn's stream carries the answer "Fixed it."
    And the workspace holds "fixed.txt"
