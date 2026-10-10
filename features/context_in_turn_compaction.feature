Feature: Compaction inside a long turn
  One prompt can start dozens of tool steps, and a small model window fills up
  with their results before the turn is over. Compaction folded only earlier
  turns, so a turn like that grew until the provider refused the request and the
  turn ended on the provider's error. Coddy now folds the earlier steps of the
  turn itself when the context reaches the threshold. The summary row that
  replaces the steps begins with the prompt, verbatim, and the latest steps stay
  as they were. (coddy-project/coddy-agent#490)

  Scenario: A single long turn stays inside the window
    Given a model window of 36000 tokens
    And result eviction is off, so only compaction can keep the turn inside the window
    And a model that reads 4 files in parallel at each of 14 steps and then answers
    And the provider refuses any request larger than the window
    When the user sends one prompt
    Then no request was refused
    And the turn ended with the model's answer
    And the turn was folded more than once
    And every request carried the prompt verbatim
    And every tool result in each request answers a call that request carries
    And the transcript still holds every original message
