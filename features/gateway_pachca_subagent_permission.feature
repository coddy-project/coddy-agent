Feature: A subagent asks for permission in the Pachca chat
  The bot allows what an admin's own agent asks; a subagent whose definition
  narrowed what it may do is asked about in the chat with buttons, and only the
  person whose session asked may answer - during the turn that spawned it, and
  after that turn ended. The conversation of somebody who is not the bot's
  admin approves nothing.

  Background:
    Given a fake Pachca workspace whose bot is "coddy_bot"
    And a pachca gateway over a scripted agent pointed at it
    And the bot is started
    And "anna" is an admin of the Pachca bot

  Scenario: A subagent of a running turn asks and a click answers it
    Given the agent's turn has a subagent "reviewer" that asks to run "go test ./..."
    When the person "anna" writes "review it" in a direct chat
    Then the direct chat with "anna" shows a permission request naming "reviewer" with the buttons "Allow" and "Reject"
    When "anna" clicks "Allow"
    Then the subagent is answered "allow"
    And the permission request reads "Allowed."

  Scenario: A background subagent asks after the reply was sent
    Given the person "anna" wrote "hello" in a direct chat and got an answer
    When the background subagent "writer" of the session of "anna" asks to run "echo checked"
    Then the direct chat with "anna" shows a permission request naming "writer" with the buttons "Allow" and "Reject"
    When "anna" clicks "Reject"
    Then the subagent is answered "reject"
    And the permission request reads "Denied."
