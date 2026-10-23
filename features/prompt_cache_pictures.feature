Feature: A session of screenshots keeps the provider's prompt cache
  A picture stays in the history and goes out with every request after it,
  within what one request carries: 20 pictures and 20 MB of them. The pictures
  a request carries are one run of the newest, which grows with every new
  picture; when it would hold more than a request takes, the oldest leave
  together, down to half of it. Between two of those evictions every request
  begins as the one before it did and only adds at its end, so the provider's
  prompt cache holds through a session of screenshots instead of breaking with
  each one.

  Scenario: Screenshots leave the request in blocks
    Given an agent session whose model reads images and an MCP server that returns a screenshot
    When the model takes 32 screenshots in one turn, then answers
    Then every request after a screenshot carries the newest one and at most 20
    And a request begins as the one before it, except after the 21st and the 32nd screenshot
