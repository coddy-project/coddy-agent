Feature: Compaction inside a long turn
  One prompt can start dozens of tool steps, and a small model window fills up
  with their results before the turn is over. Compaction folded only earlier
  turns, so a turn like that grew until the provider refused the request and the
  turn ended on the provider's error. Coddy now folds the earlier steps of the
  turn itself when the context reaches the threshold, and when the provider
  refuses a request as larger than its window it compacts the turn once and
  asks again. The summary row that replaces the steps begins with the prompt,
  verbatim, and the latest steps stay as they were. (coddy-project/coddy-agent#490)

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

  Scenario: A follow-up typed during a long turn stays with the prompt
    Given a model window of 36000 tokens
    And result eviction is off, so only compaction can keep the turn inside the window
    And a model that reads 4 files in parallel at each of 14 steps and then answers
    And the provider refuses any request larger than the window
    And the user types the follow-up "FOLLOW-UP: keep every finding under two lines" while the model is at step 3
    When the user sends one prompt
    Then no request was refused
    And the turn ended with the model's answer
    And the turn was folded more than once
    And every request carried the prompt verbatim
    And every request from the first that read the follow-up carried it once
    And every tool result in each request answers a call that request carries

  Scenario: A refused oversized request is asked again after compaction
    Given a model window of 128000 tokens that the provider serves only up to 30000
    And result eviction is off, so only compaction can keep the turn inside the window
    And a model that reads 4 files in parallel at each of 5 steps and then answers
    And the provider refuses any request larger than what it serves
    When the user sends one prompt
    Then the provider refused one request as larger than its window
    And the turn was compacted inside the turn once
    And the request sent again is smaller than the refused one and carries the prompt
    And the turn ended with the model's answer
    And the transcript holds a summary row that starts with the prompt
    And the transcript still holds every original message
