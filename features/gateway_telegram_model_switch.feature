Feature: Switching the LLM model from a Telegram chat
  /model answers with an inline keyboard, one button per configured model, and
  the tap carries the choice back as callback_data. Telegram caps that payload
  at 64 bytes, and the button has to survive a gateway restart: the chat keeps
  showing the keyboard long after the process that sent it is gone.

  The whole path runs at debug, from the update arriving to the model being
  applied, because a tap that does nothing leaves no other trace - the chat
  shows a spinner that clears itself and the log stays silent.

  Background:
    Given a telegram gateway with the models "openai/gpt-4o" and "rpa/qwen3.6-35b-a3b"

  Scenario: Tapping a model button changes the session model
    When the user sends "/model"
    And the user taps the button for "rpa/qwen3.6-35b-a3b"
    Then the session model is "rpa/qwen3.6-35b-a3b"

  Scenario: A button tapped after a gateway restart still applies
    Given the user has been offered the model keyboard
    And the gateway is restarted with the session left on disk
    When the user taps the button for "rpa/qwen3.6-35b-a3b"
    Then the session model is "rpa/qwen3.6-35b-a3b"

  Scenario: A model id too long for callback_data still round-trips
    Given a model whose id is longer than the telegram callback limit
    When the user sends "/model"
    And the user taps the button for that long model
    Then the session model is that long model

  Scenario: The debug trail records the command and the tap
    Given the component "gateway.telegram" is configured at "debug"
    When the user sends "/model"
    And the user taps the button for "rpa/qwen3.6-35b-a3b"
    Then the log records the command "model"
    And the log records the model menu with the session id
    And the log records the callback with the resolved model "rpa/qwen3.6-35b-a3b"
    And the log records that the model was applied
