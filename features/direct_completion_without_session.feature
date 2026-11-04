Feature: A direct completion without a session leaves nothing behind
  A client that uses Coddy as an OpenAI-compatible provider sends one request
  per model step and brings the whole conversation every time. Without
  X-Coddy-Session-ID such a request is one stateless model call: nothing is
  written and no session is left. The header is how a client asks for one.

  Scenario Outline: a call without the header writes no session
    Given a coddy server with a direct model
    When a client posts the direct model to "<endpoint>" with stream <stream> and no session header
    Then the client gets the model's answer
    And the answer names no session
    And the session store is empty
    And the session listing is empty

    Examples:
      | endpoint             | stream |
      | /v1/chat/completions | false  |
      | /v1/chat/completions | true   |
      | /v1/responses        | false  |
      | /v1/responses        | true   |

  Scenario: a tool loop without the header leaves nothing and keeps its conversation
    Given a coddy server with a direct model
    When a client posts a tool call step and then its tool result with no session header
    Then the model received the call and its result in the second request
    And the session store is empty

  Scenario Outline: a call with the header keeps its session
    Given a coddy server with a direct model
    When a client posts the direct model to "<endpoint>" with session "sess_direct_kept"
    Then the client gets the model's answer
    And session "sess_direct_kept" is stored with the question and the answer

    Examples:
      | endpoint             |
      | /v1/chat/completions |
      | /v1/responses        |

  Scenario: a profile turn without the header still creates its session
    Given a coddy server with a direct model
    When a client posts the agent profile with no session header
    Then the answer names a stored session
