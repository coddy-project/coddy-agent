Feature: A Telegram admin opens the web UI as the bot's Mini App
  The web UI opened from the Telegram bot carries launch data Telegram signed
  with the bot's token. When the person is one of the bot's admins, that
  signature signs the browser in, with no password; anybody else is refused
  and sees the ordinary sign-in form, because the web UI is the whole agent.

  Background:
    Given a web UI behind a sign-in form with a Telegram bot whose admin is 4242

  Scenario: The bot's admin is signed in by the launch data
    When the Mini App posts launch data signed for user 4242
    Then the sign-in is accepted
    And the browser reaches the API with its cookie
    And the server reports the browser signed in as "telegram:4242"

  Scenario: Somebody who is not an admin is refused
    When the Mini App posts launch data signed for user 5151
    Then the sign-in is refused with status 403
    And the browser does not reach the API
