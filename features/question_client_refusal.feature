Feature: A question the client refuses is reported to the model
  The question tool asks the user through the client: an editor that renders
  no elicitation form gets a session/request_question call on the wire, and a
  client that cannot ask answers it with a protocol error. That refusal is not
  an answer from the user, so it has to reach the model as the tool's error -
  an empty set of answers would read as "the user answered nothing", and the
  agent would go on guessing instead of asking another way.

  Scenario: A client that cannot ask refuses the question and the model hears it
    Given an ACP client that does not advertise elicitation
    When the question tool asks "Which format?"
    And the client refuses the question with error -32601 "Method not found"
    Then the question tool reports an error naming "-32601 Method not found"

  Scenario: A client that answers still passes the answer through
    Given an ACP client that does not advertise elicitation
    When the question tool asks "Which format?"
    And the client answers "JSON"
    Then the question tool returns the answer "JSON"
