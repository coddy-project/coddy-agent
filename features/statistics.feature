Feature: Persistent usage statistics
  Scenario: Usage stays available after chats are removed and old counters can be cleared
    Given a statistics store with calls on two days
    When I reopen the statistics store
    Then the recorded usage is 240 tokens in one chat
    And the cached input is counted within input tokens
    When I clear statistics except the last day
    Then the recorded usage is 120 tokens in one chat
    When the old event is imported again
    Then the recorded usage is 120 tokens in one chat

  Scenario: A swarm ring contributes each dataset once
    Given two routes to the same statistics dataset
    When I merge their statistics
    Then the recorded usage is 120 tokens in one chat
