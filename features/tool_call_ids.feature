Feature: A tool call id belongs to one call of its session
  The id of a tool call comes from the model's provider, and everything Coddy
  keeps about a call is found by it: the row a client draws and the permission
  prompt under that row, the record under tool_calls/, the result paired with
  the call in the history. Some models write the id themselves, as
  "functions.write:0", and can hand out one that an earlier call of the session
  already carries. The web UI then drew the newest call, and the prompt waiting
  on it, on the row of the old call far up the transcript (issue #567). Coddy
  gives such a call an id of its own, the provider's id with a numeric suffix,
  before anything sees the call.

  Scenario: A call whose id an earlier step used gets an id of its own
    Given a session bundle in ask mode and a model that names every call "functions.write:0"
    When the model writes "a.txt" in one step and "b.txt" in the next
    Then the calls are announced under the ids "functions.write:0" and "functions.write:0_2"
    And the permission prompts name the calls "functions.write:0" and "functions.write:0_2"
    And the bundle keeps a record for each call with its own arguments
    And the history pairs each result with its own call
    And the turn ended with the model's answer
