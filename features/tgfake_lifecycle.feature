Feature: The offline Telegram stand preserves update and preview lifetimes
  The stand applies subscriptions when updates are created and expires rich
  previews, so debugging observes the same lifecycle as the Bot API.

  Scenario: Changing a subscription preserves an already queued message
    Given an offline Telegram stand
    And a user message is queued
    When the bot subscribes to callback queries only
    Then the queued message is delivered

  Scenario: A rich preview disappears after its lifetime
    Given an offline Telegram stand
    When the bot streams a rich preview
    Then the chat shows 1 rich preview
    When 30 seconds pass without a new revision
    Then the chat shows 0 rich previews
