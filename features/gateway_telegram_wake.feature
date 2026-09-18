Feature: A turn woken in a chat's session is delivered to the chat
  A Telegram conversation is an ordinary session, so its agent can start a
  background task with notify_on_finish and end its turn. When the task ends the
  woken turn belongs to the chat: it runs there, through the chat's own sender,
  so the person reads what woke the agent and then the answer, in the chat where
  they asked for the work - whether or not a browser is watching the same
  session, and with no HTTP server in the process at all.

  Scenario: The chat receives the wake note and the answer of the woken turn
    Given a telegram chat whose agent is a scripted model
    When the user asks the agent to "start the tests" in the background
    Then the chat shows "Started the tests in the background."
    When the background task ends with exit code 2
    Then the chat shows "Woken by a finished background task: bg_1"
    And the chat shows "failed, exit 2"
    And the chat shows "The tests failed with exit 2."
    And the session keeps the woken turn's first message as a background wake
