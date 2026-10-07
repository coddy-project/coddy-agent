Feature: The Pachca bot reads its events history and answers in the chat
  A Pachca integration bot gets every event of the chats it is in through its
  events history: the bot reads the log, newest first, down to the last event
  it handled, answers what is addressed to it, oldest first, and deletes what
  it handled. In a direct chat every message is for the bot; in a group chat,
  where many people talk, the bot answers only a mention or a reply to its own
  message. A reply brings the message it answers along, quoted in front of what
  the person wrote, so the agent knows what the question is about. The answer
  streams into one message that ends as the rendered text. Which server the bot talks to comes from
  CODDY_PACHCA_API_BASE, so a stand-in on this machine can take Pachca's place.

  Background:
    Given a fake Pachca workspace whose bot is "coddy_bot"
    And a pachca gateway over a scripted agent pointed at it

  Scenario: Starting the bot introduces it to Pachca
    When the bot is started
    Then Pachca received "GET /oauth/token/info"
    And Pachca received "GET /profile"
    And the bot knows itself as "coddy_bot"

  Scenario: A direct message is answered in the direct chat
    Given the agent answers with "**plain** answer"
    When the bot is started
    And the person "anna" writes "hello" in a direct chat
    Then the direct chat with "anna" shows a bot message "**plain** answer"
    And the agent was asked "hello"
    And the events history no longer holds that message

  Scenario: A mention in a group chat is answered as a reply
    Given the agent answers with "group answer"
    When the bot is started
    And the person "boris" writes "@coddy_bot what is up?" in the group "dev"
    Then the group "dev" shows a bot message "group answer" replying to that message
    And the agent was asked "what is up?"

  Scenario: A group message that does not address the bot gets nothing
    When the bot is started
    And the person "boris" writes "lunch at noon?" in the group "dev"
    And the bot has read the events history
    Then the group "dev" shows no bot message
    And the agent was asked nothing

  Scenario: A reply to the bot's message continues the conversation
    Given the agent answers with "first"
    When the bot is started
    And the person "boris" writes "@coddy_bot start" in the group "dev"
    And the group "dev" shows a bot message "first" replying to that message
    And the agent answers with "second"
    And the person "boris" replies "and then?" to the bot's last message in the group "dev"
    Then the group "dev" shows a bot message "second" replying to that message

  Scenario: A reply in a direct chat asks about the quoted message
    Given the agent answers with "pong"
    When the bot is started
    And the person "anna" writes "ping" in a direct chat
    And the direct chat with "anna" shows a bot message "pong"
    And the person "anna" replies "why?" to the bot's last message in the direct chat
    Then the agent was asked:
      """
      > Coddy:
      > pong

      why?
      """

  Scenario: A mention that replies to a colleague brings that message along
    Given the agent answers with "it is the linter"
    When the bot is started
    And the person "boris" writes "the build is red" in the group "dev"
    And the person "anna" replies "@coddy_bot what happened?" to that message in the group "dev"
    Then the group "dev" shows a bot message "it is the linter" replying to that message
    And the agent was asked:
      """
      > Boris:
      > the build is red

      what happened?
      """

  Scenario: The bot's own messages are not answered
    Given the agent answers with "only once"
    When the bot is started
    And the person "anna" writes "ping" in a direct chat
    And the direct chat with "anna" shows a bot message "only once"
    And the bot has read the events history
    Then the agent was asked 1 time

  Scenario: A restart catches up on what was written while the bot was down
    Given the agent answers with "caught up"
    When the bot is started
    And the bot is stopped
    And the person "anna" writes "while you slept" in a direct chat
    And the bot is started again
    Then the direct chat with "anna" shows a bot message "caught up"
    And the agent was asked "while you slept"

  Scenario: The API base comes from the environment
    Given the environment names the fake as the Pachca API base
    When the bot is started
    Then Pachca received "GET /oauth/token/info"
