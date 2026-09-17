Feature: Context overflow protection for read and grep results
  Paging a large file or running wide searches would otherwise pin every result
  in the LLM context forever. Unmarked read/grep results are collapsed to short
  placeholders when building the request, while the model keeps the ones it marks
  as useful. Tool output is also capped by a per-tool line limit. The persisted
  transcript always keeps every result in full.

  It holds off until the conversation actually needs the room: a placeholder that
  appears mid-history invalidates every token the provider cached behind it, so a
  short session is sent untouched.

  Scenario: A short conversation reaches the model with every result intact
    Given a workspace file "big.go" with 30 numbered lines
    And result eviction starts at 50 percent of the context window
    When the model reads page 1, reads page 2, reads page 3, then answers
    Then the next LLM request keeps all three pages verbatim

  Scenario: A marked read page survives while unmarked pages are evicted
    Given a workspace file "big.go" with 30 numbered lines
    When the model reads page 1, reads page 2, marks page 2 as useful, reads page 3, then answers
    Then the next LLM request keeps page 2 verbatim
    And the next LLM request replaces page 1 and page 3 with placeholders
    And the next LLM request has one tool result per tool call
    And the persisted transcript still contains all three pages in full

  Scenario: A marked grep result survives while an unmarked one is evicted
    Given a workspace with files matching "alphaMATCH" and "betaMATCH"
    When the model greps for "alphaMATCH", greps for "betaMATCH", marks the "alphaMATCH" search as useful, then answers
    Then the next LLM request keeps the "alphaMATCH" results verbatim
    And the next LLM request replaces the "betaMATCH" results with a placeholder
    And the persisted transcript still contains both grep results in full

  Scenario: A wide grep result is capped by the tool output limit
    Given a workspace file "dups.txt" with 300 lines matching "dup"
    And the grep output limit is 5 lines
    When the model greps for "dup"
    Then the grep result shows at most 5 matching lines
    And the grep result ends with a truncation marker
