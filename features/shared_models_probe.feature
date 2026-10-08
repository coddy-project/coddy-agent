Feature: A shared-model call is cut when its client stops saying it is alive
  A client that asks for the application probe (X-Coddy-Probe: 1) is
  confirmed with an id and pings POST /coddy/llm/alive while its stream runs.
  The remote arms a guard at the first ping it accepts and cancels the call,
  freeing its slot, when more than the grace passes without one. A call that
  did not ask is never cut by it, and a path that carries no ping at all cuts
  nothing: the guard is armed by a ping, not by the start of the call.

  Scenario: A client that pings keeps its call, one that goes silent loses it
    Given a remote coddy with the probe grace of 300 ms and a call that asked for the probe
    When the client pings every 100 ms for 1 second
    Then the call still holds its slot
    When the client goes silent
    Then the call is cut and its slot is free within 3 seconds
    And the stream ends with the terminal error "probe_lapsed"

  Scenario: A path that carries no ping cuts nothing
    Given a remote coddy with the probe grace of 300 ms and a call that asked for the probe
    When the client never pings, for 2 seconds
    Then the call still holds its slot

  Scenario: A call that did not ask is never cut
    Given a remote coddy with the probe grace of 300 ms and a call that did not ask for the probe
    When the client never pings, for 2 seconds
    Then the call still holds its slot
