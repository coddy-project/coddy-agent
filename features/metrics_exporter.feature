Feature: Prometheus exporter
  As an operator I point Prometheus at coddy serve and read how the process is
  doing - turns, provider attempts, the disk of the session store - from a
  listener of its own, without the HTTP API and without a name, a title or a
  prompt of anybody's.

  Scenario: a scrape after one turn, without the HTTP API
    Given coddy serve built with metrics, the exporter on a free port with the token "scrape-me"
    And a provider "stub" served by llmstub with the model "stub/echo" declared
    And a session titled "SECRET-TITLE" in the folder "SECRET-WS" ran one turn with "SECRET-PROMPT" that read a file
    When Prometheus scrapes "/metrics" with the token "scrape-me"
    Then "coddy_build_info" is 1 and "coddy_llm_attempts_total" for stub, echo, ok rose by 2
    And "coddy_tool_calls_total" for read, ok rose by 1
    And "coddy_turns_started_total" and "coddy_turns_ended_total" each rose by 1
    And "coddy_turn_duration_seconds_count" rose by 1
    And "coddy_llm_time_to_first_token_seconds_count" rose by 2
    And "coddy_prompts_pending" is present
    And "coddy_sessions_disk_available_bytes" is present
    And the answer contains none of "SECRET-TITLE", "SECRET-WS", "SECRET-PROMPT"

  Scenario: the last hour is sampled from the exporter's own counters
    Given coddy serve built with metrics, the exporter on a free port with the token "scrape-me"
    And a provider "stub" served by llmstub with the model "stub/echo" declared
    And a session titled "SECRET-TITLE" in the folder "SECRET-WS" ran one turn with "SECRET-PROMPT" that read a file
    When the sampler's clock reaches the next slot, 15 seconds after its first read
    And Prometheus scrapes "/metrics" with the token "scrape-me"
    Then the status document shows "attemptsOk" at 8 per minute in its last point and a gap before it
    And its provider row "stub" counts 2 attempts and 0 failures, the last success at that slot
    And the scrape counts the same 2 attempts for "coddy_llm_attempts_total" stub, echo, ok
