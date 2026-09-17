Feature: A streamed answer that goes silent is cut after a bounded idle time
  A provider that accepts the request and then stops sending mid-way leaves
  the turn waiting forever: nothing on the wire says the answer will never
  finish, and the first-token guard has already been satisfied. The stream
  idle guard (agent.llm_stream_idle_timeout_ms) cuts a stream whose server
  sent nothing for that long after its first bytes, keeps the text already
  delivered next to a stall error that names the idle time, and retries the
  request only while nothing reached the caller, so no text is streamed
  twice. The wait for the first byte is not the guard's business: that is
  the first-token guard's, and a blocking (stream: false) answer arrives in
  one piece and is never guarded.

  Scenario: A stream that stalls after text deltas fails with a stall error and keeps the partial text
    Given an "openai" provider with a stream idle timeout of 200 ms pointed at a stub server that stalls after text deltas
    When a streaming completion is requested
    Then the call fails with a stall error that names the idle time
    And the partial response preserves text "Hello fr"
    And the stub server received 1 request

  Scenario: A stream that stalls before any delta is retried and succeeds
    Given an "openai" provider with a stream idle timeout of 200 ms whose upstream stalls once after an empty first frame and then streams a completion
    When a streaming completion is requested
    Then the call succeeds with text "Hello after retry" in 2 upstream requests
