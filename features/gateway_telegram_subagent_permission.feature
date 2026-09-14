Feature: A subagent asks for permission in the Telegram chat
  The bot allows what the chat's own agent asks without a question: the admin
  who configured the bot decided that. A subagent is different. Its definition
  may have narrowed what it is allowed to do, and a background one keeps working
  after the reply was sent. So, like the web UI and the console, the bot asks the
  person in the chat, names the subagent, and waits for a tap - during the turn
  that spawned it, and after that turn ended, in the chat that owns the parent
  session. A denial refuses that one call; the subagent carries on and reports
  what it could not do.

  Scenario: A subagent of a running turn asks in the chat and a tap answers it
    Given a telegram chat whose agent is running a turn
    When a subagent "reviewer" of that turn asks to run "go test ./..."
    Then the chat shows a permission request naming the subagent "reviewer" with the buttons "Allow" and "Reject"
    When the user taps "Allow"
    Then the subagent is answered "allow"
    And the request in the chat reads "Allowed"

  Scenario: A background subagent asks in the chat after the reply was sent
    Given a telegram chat with a session
    When the background subagent "writer" of that session asks to run "echo checked"
    Then the chat shows a permission request naming the subagent "writer" with the buttons "Allow" and "Reject"
    When the user taps "Reject"
    Then the subagent is answered "reject"
    And the request in the chat reads "Denied"
