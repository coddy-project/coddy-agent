Feature: Request latency diagnostics
  Scenario: Debug diagnostics correlate model calls and tool dispatch without content
    Given an agent with debug request diagnostics
    When a scripted model calls a tool and then answers
    Then the request timings and completion records share unique call identifiers
    And the diagnostics contain no request or response content
