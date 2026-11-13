Feature: A compaction reads the conversation out of the provider's prompt cache
  Folding a history used to be a request of its own: a summarizer system prompt and the
  transcript flattened into one user message, which shares no byte with what the session
  had been sending. The provider read the whole head again at full price, and in a long
  session the folds cost more than the turns that triggered them. When the summarizer is
  the session's own model, Coddy now asks with the conversation as the turn sent it - the
  same system message, the same tools, the same messages up to the boundary - and appends
  the summarization instruction as the last message. Everything in front of that
  instruction is the prefix the provider has just cached, so a fold reads it back instead
  of paying for it. The chain of earlier summary rows travels as the messages it already
  is, rather than as a block of text rebuilt for the request.

  Scenario: The summarization request continues the request the turn last sent
    Given an agent session in a workspace
    When the model works through a few turns
    And the operator compacts the conversation
    Then the summarization request repeats, message for message, what the turn had already sent
    And the summarization instruction is the last message of the summarization request
    And every other message of the summarization request is one the session had sent

  Scenario: The summarizer is asked with the tools and the system message of the turn
    Given an agent session in a workspace
    When the model works through a few turns
    And the operator compacts the conversation
    Then the summarization request opens with the system message of the turn
    And the summarization request carries the tool definitions of the turn

  Scenario: The summary rows of the chain travel as the messages they already are
    Given an agent session in a workspace
    When the model works through a few turns
    And the operator compacts the conversation
    And the model works through a few turns
    And the operator compacts the conversation
    Then the second summarization request carries the first summary row as a message of its own
    And the second summarization request repeats, message for message, what the turn had already sent
    And no summarization request carries the earlier summaries as a block of text

  Scenario: A summarizer on another model reads the flattened transcript
    Given an agent session whose summarizer is another model
    When the model works through a few turns
    And the operator compacts the conversation
    Then the summarization request is the flattened transcript under its own system message
    And the summarization request carries no tool definitions

  Scenario: Collapsing results in batches leaves the sent history alone between batches
    Given an agent session in a workspace of many files
    When the model reads one file per step until results are collapsed twice
    Then every request repeats the previous one up to the first result it collapsed
    And a request that collapses results is followed by requests that only grow
