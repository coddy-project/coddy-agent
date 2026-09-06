Feature: Operator hooks at the turn and session boundaries
  Beyond tool calls, hooks fire when the user submits a prompt, when the agent is about to
  stop, when a session starts, and before and after context compaction. A prompt hook can
  reject the prompt or add context; a stop hook can send the agent back to work with a
  follow-up, bounded by hooks.stop_loop_limit; a session-start hook adds context that every
  system prompt of the session carries; a pre-compaction hook can veto a compaction.

  Scenario: A UserPromptSubmit hook rejects a prompt
    Given the operator's hooks.json has a UserPromptSubmit hook that blocks with the reason "no secrets in prompts"
    And an agent session
    When the user sends the prompt "here is my password"
    Then the turn is refused with an error mentioning "no secrets in prompts"
    And the model was never called
    And the transcript holds no user message "here is my password"

  Scenario: A UserPromptSubmit hook adds context the model reads
    Given the operator's hooks.json has a UserPromptSubmit hook that adds the context "the release branch is frozen"
    And an agent session
    When the user sends the prompt "what should I work on"
    Then the model's system prompt contains "the release branch is frozen"
    And the transcript holds the user message "what should I work on" unchanged

  Scenario: A Stop hook sends the agent back to work once
    Given the operator's hooks.json has a Stop hook that blocks with the reason "run the tests first" unless the stop hook is already active
    And an agent session
    When the model answers "all done" and, after the follow-up, answers "tests are green"
    Then the model was called 2 times
    And the transcript holds a user message "[Stop hook] run the tests first"
    And the transcript ends with the assistant answer "tests are green"

  Scenario: The stop loop limit ends the turn
    Given the operator's hooks.json has a Stop hook that always blocks with the reason "again"
    And the hooks stop loop limit is 2
    And an agent session
    When the model keeps answering "done"
    Then the model was called 3 times
    And the turn ended with the stop reason "end_turn"

  Scenario: A SessionStart hook adds context to every system prompt of the session
    Given the operator's hooks.json has a SessionStart hook that adds the context "project codename: heron"
    And an agent session
    When the user sends the prompt "hello"
    Then the model's system prompt contains "project codename: heron"
    And the session bundle records the hook context "project codename: heron"

  Scenario: A PreCompact hook vetoes a manual compaction
    Given the operator's hooks.json has a PreCompact hook that blocks with the reason "not now"
    And an agent session with a long transcript
    When the user runs /compact
    Then the compaction is refused with an error mentioning "not now"
    And the transcript was not compacted

  Scenario: A PostCompact hook sees the trigger and the summary
    Given the operator's hooks.json has a PostCompact hook that records its stdin
    And an agent session with a long transcript
    When the user runs /compact and the model summarises with "SUMMARY OF THE WORK"
    Then the recorded payload names the event "PostCompact" with the trigger "manual"
    And the recorded payload carries a summary mentioning "SUMMARY OF THE WORK"
