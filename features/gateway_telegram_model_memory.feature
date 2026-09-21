Feature: Model memory on the Telegram gateway
  The gateway is a surface of its own: a fresh chat session starts on the
  model an operator last picked on this bot, and the bot's very first use
  starts on the alphabetically first configured model. A cleared conversation
  counts as fresh: /clear mints a new session that follows the same rule.

  Scenario: A first fresh session starts on the alphabetically first model
    Given a telegram gateway with the models "zed/m" and "aaa/m"
    When the user sends "hello"
    Then the chat's session model is "aaa/m"

  Scenario: A model picked on one chat greets the next fresh chat
    Given a telegram gateway with the models "openai/gpt-4o" and "rpa/qwen3.6-35b-a3b"
    When the user sends "/model"
    And the user taps the button for "rpa/qwen3.6-35b-a3b"
    And the second chat sends "hello"
    Then the second chat's session model is "rpa/qwen3.6-35b-a3b"

  Scenario: A cleared conversation starts on the remembered model
    Given a telegram gateway with the models "openai/gpt-4o" and "rpa/qwen3.6-35b-a3b"
    When the user sends "/model"
    And the user taps the button for "rpa/qwen3.6-35b-a3b"
    And the user sends "/clear"
    And the user sends "hello again"
    Then the chat's session model is "rpa/qwen3.6-35b-a3b"

  Scenario: A typed session-scoped /model is remembered too
    Given a telegram gateway with the models "openai/gpt-4o" and "rpa/qwen3.6-35b-a3b"
    When the user sends "/model rpa/qwen3.6-35b-a3b"
    And the second chat sends "hello"
    Then the second chat's session model is "rpa/qwen3.6-35b-a3b"

  Scenario: A turn-scoped --once pick is not remembered
    Given a telegram gateway with the models "openai/gpt-4o" and "rpa/qwen3.6-35b-a3b"
    When the user sends "/model rpa/qwen3.6-35b-a3b --once"
    And the second chat sends "hello"
    Then the second chat's session model is "openai/gpt-4o"
