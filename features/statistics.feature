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

  @http
  Scenario: The statistics of a server are read, tuned and cleared over REST
    Given a coddy HTTP server with recorded usage
    When I read the statistics over REST
    Then the report carries the recorded tokens
    When I save the statistics time zone over REST
    And I save a custom tariff over REST
    Then the saved time zone and the custom tariff are both kept
    And the daily export carries the recorded tokens
    When I clear the statistics over REST
    Then the report carries no chats

  Scenario: A swarm ring contributes each dataset once
    Given two routes to the same statistics dataset
    When I merge their statistics
    Then the recorded usage is 120 tokens in one chat

  @http
  Scenario: The display currency defaults to USD at the manual rate
    Given a coddy HTTP server with recorded usage
    When I read the statistics over REST
    Then the statistics settings default to USD and the USD/RUB rate of 90

  @http
  Scenario: The display currency and USD/RUB rate are saved
    Given a coddy HTTP server with recorded usage
    When I save the RUB display currency with a USD/RUB rate of 90
    Then the statistics settings keep RUB and the USD/RUB rate of 90

  @http
  Scenario: Custom model tariffs can be denominated in RUB
    Given a coddy HTTP server with recorded usage
    When I save a RUB tariff for the model "test"
    Then the custom tariff for "test" is stored in RUB
