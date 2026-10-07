Feature: The Telegram bot offers the web UI as its Mini App
  With gateways.telegram.mini_app.url set, the bot points its menu button at
  the web UI and answers /app with a button that opens the chat's own
  conversation there. A conversation in a chat is an ordinary session, so the
  web UI shows the same transcript, live. The bot takes back only a menu
  button it set itself.

  Scenario: The menu button opens the web UI
    Given a fake Bot API and a gateway whose Mini App is https://coddy.example.com/
    When the bot starts
    Then the bot's menu button opens https://coddy.example.com/ as a Mini App

  Scenario: /app opens the chat's own conversation
    Given a fake Bot API and a gateway whose Mini App is https://coddy.example.com/
    And the bot is running
    And the person has talked to the bot
    When the person sends /app
    Then the bot answers with a button that opens the chat's session in https://coddy.example.com/ as a Mini App

  Scenario: A bot that no longer offers a Mini App takes its menu button back
    Given a fake Bot API and a gateway whose Mini App is https://coddy.example.com/
    And the bot has run once and set its menu button
    When the bot starts again without a Mini App
    Then the bot's menu button shows its commands again
