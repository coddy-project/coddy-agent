Feature: The Telegram bot polls a Bot API server
  Starting the bot is a conversation with the Bot API: it introduces itself
  with getMe, publishes its command list, then long-polls getUpdates and hands
  whatever arrives to the same handlers a chat command goes through. Which
  server it talks to comes from CODDY_TELEGRAM_API_BASE, the origin the
  --dry-run probe already honours, so a stand-in on the same machine can take
  the place of api.telegram.org: for a test, and for an operator debugging a
  bot without a phone.

  Background:
    Given a fake Bot API whose bot is "coddy_fake_bot"
    And a telegram gateway over a scripted agent pointed at it

  Scenario: Starting the bot introduces it to the Bot API
    When the bot is started
    Then the Bot API received "getMe"
    And the Bot API received "setMyCommands"
    And the bot knows itself as "coddy_fake_bot"

  Scenario: A private message is answered through polling
    Given the agent answers with "plain answer"
    When the bot is started
    And the user sends "hello"
    Then the chat shows a bot message containing "plain answer"
    And the next poll confirms that update

  Scenario: A model button tapped in the chat is applied through polling
    When the bot is started
    And the user sends "/model"
    And the user taps the button for "rpa/qwen3.6-35b-a3b"
    Then the Bot API received "answerCallbackQuery"
    And the keyboard message marks "rpa/qwen3.6-35b-a3b" as current
    And the session model is "rpa/qwen3.6-35b-a3b"

  Scenario: A tap arrives whatever subscription a previous bot left behind
    Telegram remembers the last allowed_updates a bot asked for. A bot this
    token once ran under another framework may have subscribed to messages
    alone; the taps would then be dropped before they reach anyone.
    Given the Bot API remembers a subscription to messages only
    When the bot is started
    And the user sends "/model"
    And the user taps the button for "rpa/qwen3.6-35b-a3b"
    Then the session model is "rpa/qwen3.6-35b-a3b"

  Scenario: Stopping the bot ends polling
    When the bot is started
    And the bot is stopped
    Then Start returned without error
