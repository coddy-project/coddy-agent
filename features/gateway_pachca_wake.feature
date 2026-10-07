Feature: A finished background task wakes the Pachca chat's session
  A chat conversation is an ordinary session, so a background task its agent
  started with notify_on_finish wakes it when it ends, and the woken turn runs
  in the chat the session belongs to. A direct chat is reached through the
  person: a Pachca direct chat id is not the person's id.

  Background:
    Given a fake Pachca workspace whose bot is "coddy_bot"
    And a pachca gateway over a scripted agent pointed at it
    And the bot is started

  Scenario: A woken turn of a direct conversation lands in that direct chat
    Given the person "anna" wrote "build it" in a direct chat and got an answer
    And the agent answers with "the build passed"
    When a background task of the session of "anna" finishes and wakes it
    Then the direct chat with "anna" shows a bot message containing "Woken"
    And the direct chat with "anna" shows a bot message "the build passed"

  Scenario: A woken turn of a group conversation lands in that group
    Given the person "boris" wrote "@coddy_bot build it" in the group "dev" and got an answer
    And the agent answers with "group build passed"
    When a background task of the session of "boris" in the group "dev" finishes and wakes it
    Then the group "dev" shows a bot message "group build passed"
