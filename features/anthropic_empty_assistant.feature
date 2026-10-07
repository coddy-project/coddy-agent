Feature: Anthropic history omits an assistant with no replayable content
  A saved signature without thinking text is not a valid Anthropic content block.

  Scenario: Continue a conversation after a signature-only assistant turn
    Given an Anthropic conversation contains a signature-only assistant turn
    When Coddy sends the next request
    Then the request contains the surrounding user messages without an assistant block
    And exactly one request reaches the provider
