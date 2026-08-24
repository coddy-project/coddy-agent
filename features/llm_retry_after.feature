Feature: Coddy honors server-requested retry pauses
  A 429 names the exact moment the rate-limit window reopens: Retry-After-Ms
  in milliseconds or Retry-After in seconds. Falling back to the local
  exponential ladder would burn every retry inside the same window and fail
  the turn, so the server-provided pause must win over the backoff.

  Scenario Outline: A 429 with a retry header delays the retry by the requested pause
    Given an "<provider>" provider whose upstream responds 429 with header "<header>" set to "<value>" and then succeeds
    When a completion is requested
    Then the call succeeds after 2 upstream requests
    And at least <pause> ms pass between the two upstream requests

    Examples:
      | provider  | header         | value | pause |
      | openai    | Retry-After    | 1     | 1000  |
      | openai    | Retry-After-Ms | 300   | 300   |
      | anthropic | Retry-After    | 1     | 1000  |
      | anthropic | Retry-After-Ms | 300   | 300   |
