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

  Scenario: A pause the capped retries could never cover fails fast as a quota reset
    The retry ladder waits at most 60 s per attempt, so a pause longer than
    the retries could add up to (three capped waits by default, none with
    retries disabled) cannot be honoured by retrying. Repeating the request
    would only burn the budget
    and end in the same 429 minutes later, so the wrapper reports the pause
    as a typed quota reset error at once, after a single upstream request,
    and the agent decides whether to wait for it (agent.wait_for_limit_reset).
    Given an "openai" provider whose upstream responds 429 with header "Retry-After" set to "600" and then succeeds
    When a completion is requested
    Then the call fails after 1 upstream request with a quota reset error
    And the reported pause is at least 600 s
