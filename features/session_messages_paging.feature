Feature: A long session is read page by page
  The web UI of a session with thousands of messages must not read the whole
  history to show its end. GET /coddy/sessions/{id}/messages serves the
  newest page first, then the pages before it on demand, each one ending where
  the window the client holds starts, with the counts that keep row numbers
  and rewind indices what a full read would give.

  Background:
    Given a running coddy HTTP server
    And a stored session of 30 turns, each a prompt, a tool step and an answer

  Scenario: The newest page comes first and opens with a prompt
    When I read the newest 10 messages of the session
    Then the page ends with the newest message
    And the page opens with the prompt of its turn
    And the window counts the prompts before the page

  Scenario: Older pages join the window without a gap or an overlap
    When I read the newest 10 messages of the session
    And I read older pages of 10 messages until the first message
    Then every message of the history was read exactly once
    And every page of tool calls lists the calls of its own messages only

  Scenario: A prompt inside a page is rewound by the index the window gives it
    When I read the newest 10 messages of the session
    And I rewind the session at the first prompt of that page
    Then the history ends right before that prompt
