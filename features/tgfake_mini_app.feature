Feature: The offline Telegram stand opens a bot's Mini App
  A bot offers its Mini App through a web_app button in a private chat or
  through its menu button. The stand keeps both the way Telegram does, signs
  the launch data with the bot's token the way Telegram signs it, and hands
  the app its launch parameters in the fragment of its address, so a Mini App
  can be developed and checked against it with no Telegram and no phone.

  Scenario: A web_app button opens a launch signed with the bot's token
    Given an offline Telegram stand a bot has called with its token
    When the bot sends the person a button that opens https://coddy.example.com/?session=sess_1 as a Mini App
    And the person opens that button
    Then the app is given https://coddy.example.com/?session=sess_1 with Telegram's launch parameters in the fragment
    And the launch data checks out against the bot's token

  Scenario: The menu button opens the bot's Mini App
    Given an offline Telegram stand a bot has called with its token
    When the bot sets its menu button to open https://coddy.example.com/
    Then the chat shows a menu button that opens https://coddy.example.com/
    And opening the menu button gives a launch that checks out against the bot's token
